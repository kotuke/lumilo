// Package gguf reads what Lumilo needs from the header of a GGUF file: the
// architecture, the trained context, the model's name and the shape of its
// attention, which sizes the KV cache. It reads the metadata only up to the
// first tokenizer.* key: converters write general.* first, then the
// architecture's keys, then the tokenizer with its large vocabulary arrays
// (llama.cpp gguf-py GGUFWriter), so a few hundred kilobytes hold all of it.
//
// The package knows no engine: llama.cpp's model list, Splash's assemblies
// and the Hugging Face variant list all read headers through it.
package gguf

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Meta is what the header says.
type Meta struct {
	Arch string // general.architecture, e.g. "qwen35"
	Type string // general.type: "model", "mmproj", "adapter"; "" in older files
	Name string // general.name, e.g. "OrcaCyber Nano 1.0 AI Defense"

	// HasContext is set when the header has <arch>.context_length;
	// Context is its value, the trained window, 0 when unreadable.
	HasContext bool
	Context    int

	Blocks   int // <arch>.block_count, MTP layers included
	NextN    int // <arch>.nextn_predict_layers: multi-token prediction layers
	Heads    int // <arch>.attention.head_count
	KVHeads  int // <arch>.attention.head_count_kv when it is one number
	KeyLen   int // <arch>.attention.key_length; 0 = embedding / heads
	ValueLen int // <arch>.attention.value_length; 0 = KeyLen
	Embed    int // <arch>.embedding_length
	// FullAttentionInterval is set by hybrids (Qwen3.5: every fourth layer
	// is attention, the others linear attention without a KV cache).
	FullAttentionInterval int
	// KVHeadsPerLayer is head_count_kv when it is an array (one entry per
	// layer, 0 for layers without attention); Recurrent marks the layers
	// without a KV cache (<arch>.attention.recurrent_layers).
	KVHeadsPerLayer []int
	Recurrent       []bool
	// SlidingWindow is <arch>.attention.sliding_window; such layers keep
	// only the window. Not used for the estimate (it errs high).
	SlidingWindow int
}

// NonTextArchs are architectures llama.cpp loads that do not generate text:
// embedding and reranking encoders, the vision projector, the audio decoder
// (names from LLM_ARCH_NAMES in libllama 0.7.0).
var NonTextArchs = map[string]bool{
	"clip": true, "bert": true, "modern-bert": true, "nomic-bert": true, "nomic-bert-moe": true,
	"neo-bert": true, "jina-bert-v2": true, "jina-bert-v3": true, "eurobert": true,
	"t5encoder": true, "wavtokenizer-dec": true,
	// A DFlash draft (llama_model_dflash) runs only beside its target.
	"dflash": true,
}

// TextModel reports whether the header is a text-generating model: general.type
// "model" (or none, in older files), an architecture that is not in
// NonTextArchs, and a context length. The OrcaCyber mmproj (clip, type
// mmproj) and gigaam-v3-e2e-rnnt (no context length) are not.
func (m Meta) TextModel() bool {
	return (m.Type == "" || m.Type == "model") && m.Arch != "" && !NonTextArchs[m.Arch] && m.HasContext
}

// AttentionLayers is how many loaded layers keep a KV cache: the blocks
// without the MTP layers (never loaded for decoding), minus the recurrent
// ones of a hybrid. OrcaCyber-Nano (qwen35): (65 - 1) / 4 = 16, as Splash's
// /status memory_plan.model.attention_layers says.
func (m Meta) AttentionLayers() int {
	layers := m.Blocks - m.NextN
	if layers <= 0 {
		return 0
	}
	switch {
	case len(m.KVHeadsPerLayer) > 0:
		n := 0
		for i, h := range m.KVHeadsPerLayer {
			if i < layers && h > 0 {
				n++
			}
		}
		return n
	case len(m.Recurrent) > 0:
		n := 0
		for i, r := range m.Recurrent {
			if i < layers && !r {
				n++
			}
		}
		return n
	case m.FullAttentionInterval > 0:
		return layers / m.FullAttentionInterval
	}
	return layers
}

// KVElements is how many K and V elements one token takes in the cache, all
// attention layers together; 0 when the header does not say enough. Times
// the bytes per element of the engine's cache format it is the KV cache of
// one token: 16 × 4 heads × (256 + 256) = 32768 for OrcaCyber-Nano.
func (m Meta) KVElements() int64 {
	key, val := m.KeyLen, m.ValueLen
	if key == 0 && m.Heads > 0 {
		key = m.Embed / m.Heads
	}
	if val == 0 {
		val = key
	}
	if key <= 0 {
		return 0
	}
	if len(m.KVHeadsPerLayer) > 0 {
		var n int64
		for i, h := range m.KVHeadsPerLayer {
			if i < m.Blocks-m.NextN {
				n += int64(h)
			}
		}
		return n * int64(key+val)
	}
	if m.KVHeads <= 0 {
		return 0
	}
	return int64(m.AttentionLayers()) * int64(m.KVHeads) * int64(key+val)
}

// DisplayName is general.name with spaces as dashes, the way file names
// spell it: "OrcaCyber-Nano-1.0-AI-Defense".
func (m Meta) DisplayName() string {
	return strings.Join(strings.Fields(m.Name), "-")
}

// ReadFile reads the header of the GGUF file at path.
func ReadFile(path string) (Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()
	return Read(f)
}

const (
	typeUint32  = 4
	typeInt32   = 5
	typeBool    = 7
	typeString  = 8
	typeArray   = 9
	typeUint64  = 10
	typeInt64   = 11
	maxKeyLen   = 1 << 16
	maxArrayLen = 1 << 16
)

var scalarSize = map[uint32]int{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}

// Read reads the metadata of a GGUF stream (format v2/v3: magic "GGUF",
// uint32 version, uint64 tensor count, uint64 KV count, then the KVs, all
// little-endian) up to the first tokenizer.* key. A stream cut after that
// point is fine: the rest is never read.
func Read(src io.Reader) (Meta, error) {
	var m Meta
	r := bufio.NewReaderSize(src, 64<<10)
	var head struct {
		Magic   [4]byte
		Version uint32
		Tensors uint64
		KVs     uint64
	}
	if err := binary.Read(r, binary.LittleEndian, &head); err != nil {
		return m, err
	}
	if string(head.Magic[:]) != "GGUF" || head.Version < 2 {
		return m, errors.New("gguf: not a GGUF v2+ file")
	}
	for i := uint64(0); i < head.KVs; i++ {
		key, err := readString(r, maxKeyLen)
		if err != nil {
			return m, err
		}
		var typ uint32
		if err := binary.Read(r, binary.LittleEndian, &typ); err != nil {
			return m, err
		}
		if strings.HasPrefix(key, "tokenizer.") {
			break
		}
		if typ == typeString {
			switch key {
			case "general.architecture", "general.type", "general.name":
				v, err := readString(r, maxKeyLen)
				if err != nil {
					return m, err
				}
				switch key {
				case "general.architecture":
					m.Arch = v
				case "general.type":
					m.Type = v
				default:
					m.Name = v
				}
				continue
			}
		}
		if m.Arch != "" && strings.HasPrefix(key, m.Arch+".") {
			done, err := m.archKey(r, strings.TrimPrefix(key, m.Arch+"."), typ)
			if err != nil {
				return m, err
			}
			if done {
				continue
			}
		}
		if err := skipValue(r, typ, 0); err != nil {
			return m, err
		}
	}
	return m, nil
}

// archKey reads the architecture key k when it is one Meta keeps; done is
// false when the value is still to be skipped.
func (m *Meta) archKey(r *bufio.Reader, k string, typ uint32) (done bool, err error) {
	ints := map[string]*int{
		"context_length":           &m.Context,
		"block_count":              &m.Blocks,
		"nextn_predict_layers":     &m.NextN,
		"attention.head_count":     &m.Heads,
		"attention.head_count_kv":  &m.KVHeads,
		"attention.key_length":     &m.KeyLen,
		"attention.value_length":   &m.ValueLen,
		"embedding_length":         &m.Embed,
		"full_attention_interval":  &m.FullAttentionInterval,
		"attention.sliding_window": &m.SlidingWindow,
	}
	if p, ok := ints[k]; ok && typ != typeArray {
		v, err := readInt(r, typ)
		if err != nil {
			return true, err
		}
		*p = v
		if k == "context_length" {
			m.HasContext = true
		}
		return true, nil
	}
	if typ != typeArray || (k != "attention.head_count_kv" && k != "attention.recurrent_layers") {
		return false, nil
	}
	var elem uint32
	var count uint64
	if err := binary.Read(r, binary.LittleEndian, &elem); err != nil {
		return true, err
	}
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return true, err
	}
	if count > maxArrayLen {
		return true, errors.New("gguf: array too long")
	}
	for i := uint64(0); i < count; i++ {
		if k == "attention.recurrent_layers" && elem == typeBool {
			b, err := r.ReadByte()
			if err != nil {
				return true, err
			}
			m.Recurrent = append(m.Recurrent, b != 0)
			continue
		}
		v, err := readInt(r, elem)
		if err != nil {
			return true, err
		}
		if k == "attention.head_count_kv" {
			m.KVHeadsPerLayer = append(m.KVHeadsPerLayer, v)
		}
	}
	return true, nil
}

// readInt reads an integer value of GGUF type typ; other scalar types are
// skipped and read as 0.
func readInt(r *bufio.Reader, typ uint32) (int, error) {
	var v int64
	var err error
	switch typ {
	case typeUint32:
		var x uint32
		err = binary.Read(r, binary.LittleEndian, &x)
		v = int64(x)
	case typeInt32:
		var x int32
		err = binary.Read(r, binary.LittleEndian, &x)
		v = int64(x)
	case typeUint64:
		var x uint64
		err = binary.Read(r, binary.LittleEndian, &x)
		v = int64(min(x, 1<<40))
	case typeInt64:
		err = binary.Read(r, binary.LittleEndian, &v)
	default:
		return 0, skipValue(r, typ, 0)
	}
	if err != nil {
		return 0, err
	}
	if v < 0 || v > 1<<40 {
		return 0, nil
	}
	return int(v), nil
}

func readString(r *bufio.Reader, limit uint64) (string, error) {
	var n uint64
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > limit {
		return "", errors.New("gguf: string too long")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return string(b), err
}

func skipValue(r *bufio.Reader, typ uint32, depth int) error {
	if n, ok := scalarSize[typ]; ok {
		_, err := r.Discard(n)
		return err
	}
	switch typ {
	case typeString:
		var n uint64
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return err
		}
		return discard(r, n)
	case typeArray:
		if depth > 2 {
			return errors.New("gguf: arrays nested too deep")
		}
		var elem uint32
		var count uint64
		if err := binary.Read(r, binary.LittleEndian, &elem); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
			return err
		}
		if n, ok := scalarSize[elem]; ok {
			return discard(r, count*uint64(n))
		}
		for i := uint64(0); i < count; i++ {
			if err := skipValue(r, elem, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("gguf: unknown value type %d", typ)
}

func discard(r *bufio.Reader, n uint64) error {
	for n > 0 {
		step := min(n, 1<<30)
		if _, err := r.Discard(int(step)); err != nil {
			return err
		}
		n -= step
	}
	return nil
}

// Sidecar prefixes: llama.cpp's -hf finds a repository's speculative
// decoding heads and drafts by these file name prefixes ("mtp-", "dflash-",
// "eagle3-", "dspark-", strings of libllama-common 0.7.0), as it finds the
// vision projector by "mmproj". None is a model to serve on its own.
var sidecarPrefixes = []string{"mtp-", "dflash-", "eagle3-", "dspark-"}

// Sidecar is the kind of a GGUF file name that is not a model of its own:
// "mmproj", "mtp", "dflash", "eagle3", "dspark", "imatrix"; "" for a model.
// A model with its MTP layers kept ("…-IQ2_S-mtp.gguf", 0.35 GB more than
// its twin) is a model.
func Sidecar(name string) string {
	low := strings.ToLower(name)
	if i := strings.LastIndexByte(low, '/'); i >= 0 {
		low = low[i+1:]
	}
	switch {
	case strings.Contains(low, "mmproj"):
		return "mmproj"
	case strings.HasPrefix(low, "imatrix") || strings.HasSuffix(low, "imatrix.gguf") || strings.HasSuffix(low, "imatrix.dat"):
		// Not "Qwen3.8-27B-iMatrix-NVFP4-MTP.gguf", a model quantized
		// with one.
		return "imatrix"
	}
	for _, p := range sidecarPrefixes {
		if strings.HasPrefix(low, p) {
			return strings.TrimSuffix(p, "-")
		}
	}
	// Drafts named after their target, e.g.
	// "OrcaCyber-Nano-1.0-AI-Defense-DFlash2-Q8_0.gguf" beside the model.
	for _, k := range []string{"dflash", "eagle3", "dspark"} {
		if strings.Contains(low, k) {
			return k
		}
	}
	return ""
}

// Tag is the variant tag of a GGUF file: its name without ".gguf", the
// split suffix and the longest of the names it starts with, e.g.
// "Mix-15.7G" for "OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf" and the
// names "OrcaCyber-Nano-1.0" (the repository) and
// "OrcaCyber-Nano-1.0-AI-Defense" (general.name, DisplayName). A name
// matches when the file name goes on with "-", "_" or "." after it. The
// whole stem when none matches.
//
// Splash takes such a tag: "owner/repo:VARIANT" picks the only GGUF whose
// name ends in -VARIANT (install/upstream.py select_gguf, lines 90-127),
// and llama.cpp's -hf matches the tag to the end of the file name too.
func Tag(file string, names ...string) string {
	s := Stem(file)
	best := ""
	for _, n := range names {
		n = strings.TrimSpace(n)
		if len(n) > len(best) && len(s) > len(n)+1 && strings.EqualFold(s[:len(n)], n) &&
			strings.ContainsRune("-_.", rune(s[len(n)])) {
			best = n
		}
	}
	if best == "" {
		return s
	}
	return s[len(best)+1:]
}

// Stem is a GGUF file name without its directory, ".gguf" and the
// -0000K-of-0000N suffix of a split file.
func Stem(file string) string {
	if i := strings.LastIndexByte(file, '/'); i >= 0 {
		file = file[i+1:]
	}
	if len(file) > 5 && strings.EqualFold(file[len(file)-5:], ".gguf") {
		file = file[:len(file)-5]
	}
	if n := len(file); n > 15 && file[n-15] == '-' && file[n-9:n-6] == "-of" && file[n-6] == '-' &&
		allDigits(file[n-14:n-9]) && allDigits(file[n-5:]) {
		file = file[:n-15]
	}
	return file
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// RepoBase is a repository's name without its owner and a "-GGUF" ending.
func RepoBase(repo string) string {
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		repo = repo[i+1:]
	}
	for _, suf := range []string{"-GGUF", "_GGUF", ".GGUF", "-gguf", "_gguf", ".gguf"} {
		repo = strings.TrimSuffix(repo, suf)
	}
	return repo
}
