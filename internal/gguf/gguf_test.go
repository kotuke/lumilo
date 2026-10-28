package gguf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

type kv struct {
	key string
	val any
}

// header builds a GGUF v3 header with the given metadata and no tensors.
func header(kvs []kv) []byte {
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { le(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	le(uint32(3))
	le(uint64(0))
	le(uint64(len(kvs)))
	for _, e := range kvs {
		str(e.key)
		switch v := e.val.(type) {
		case string:
			le(uint32(typeString))
			str(v)
		case uint32:
			le(uint32(typeUint32))
			le(v)
		case []bool:
			le(uint32(typeArray))
			le(uint32(typeBool))
			le(uint64(len(v)))
			for _, x := range v {
				le(x)
			}
		case []int32:
			le(uint32(typeArray))
			le(uint32(typeInt32))
			le(uint64(len(v)))
			for _, x := range v {
				le(x)
			}
		case []string:
			le(uint32(typeArray))
			le(uint32(typeString))
			le(uint64(len(v)))
			for _, s := range v {
				str(s)
			}
		default:
			panic(fmt.Sprintf("kv %s: %T", e.key, v))
		}
	}
	return b.Bytes()
}

// orca is the header of OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf as far
// as Meta reads it (values read from the file on 2026-10-27).
func orca(extra ...kv) []byte {
	kvs := []kv{
		{"general.architecture", "qwen35"},
		{"general.type", "model"},
		{"general.name", "OrcaCyber Nano 1.0 AI Defense"},
		{"general.tags", []string{"multimodal", "vision"}},
		{"qwen35.block_count", uint32(65)},
		{"qwen35.context_length", uint32(262144)},
		{"qwen35.embedding_length", uint32(5120)},
		{"qwen35.attention.head_count", uint32(24)},
		{"qwen35.attention.head_count_kv", uint32(4)},
		{"qwen35.attention.key_length", uint32(256)},
		{"qwen35.attention.value_length", uint32(256)},
		{"qwen35.nextn_predict_layers", uint32(1)},
	}
	kvs = append(kvs, extra...)
	kvs = append(kvs, kv{"tokenizer.ggml.model", "gpt2"})
	return header(kvs)
}

func TestReadOrca(t *testing.T) {
	rec := make([]bool, 65)
	for i := range rec {
		rec[i] = (i+1)%4 != 0
	}
	for name, data := range map[string][]byte{
		"interval":  orca(kv{"qwen35.full_attention_interval", uint32(4)}),
		"recurrent": orca(kv{"qwen35.attention.recurrent_layers", rec}),
	} {
		m, err := Read(bytes.NewReader(data))
		if err != nil {
			t.Fatal(name, err)
		}
		if !m.TextModel() || m.Context != 262144 || m.DisplayName() != "OrcaCyber-Nano-1.0-AI-Defense" {
			t.Errorf("%s: %+v", name, m)
		}
		// Splash's /status: attention_layers 16, kv_heads 4, head_dimension 256.
		if got := m.AttentionLayers(); got != 16 {
			t.Errorf("%s: attention layers %d, want 16", name, got)
		}
		if got := m.KVElements(); got != 16*4*512 {
			t.Errorf("%s: KV elements %d, want 32768", name, got)
		}
	}

	// A per-layer head count: layers with 0 heads keep no cache.
	m, err := Read(bytes.NewReader(header([]kv{
		{"general.architecture", "lfm2"},
		{"lfm2.block_count", uint32(4)},
		{"lfm2.context_length", uint32(1000)},
		{"lfm2.attention.head_count", uint32(8)},
		{"lfm2.embedding_length", uint32(512)},
		{"lfm2.attention.head_count_kv", []int32{0, 2, 0, 2}},
	})))
	if err != nil || m.AttentionLayers() != 2 || m.KVElements() != 4*128 {
		t.Errorf("per layer: %+v %d %d %v", m, m.AttentionLayers(), m.KVElements(), err)
	}

	// A truncated vocabulary is never read; a tokenizer key ends the read.
	cut := orca()
	if _, err := Read(bytes.NewReader(cut[:len(cut)-2])); err != nil {
		t.Error("cut:", err)
	}
	for name, data := range map[string][]byte{
		"empty": nil,
		"v1":    append([]byte("GGUF\x01\x00\x00\x00"), make([]byte, 16)...),
	} {
		if _, err := Read(bytes.NewReader(data)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTagAndSidecar(t *testing.T) {
	for _, c := range []struct {
		file  string
		names []string
		want  string
	}{
		{"OrcaCyber-Nano-1.0-AI-Defense-Mix-15.7G.gguf", []string{"OrcaCyber-Nano-1.0", "OrcaCyber-Nano-1.0-AI-Defense"}, "Mix-15.7G"},
		{"Q8_0/Qwen3-0.6B-Q8_0-00001-of-00002.gguf", []string{"Qwen3-0.6B"}, "Q8_0"},
		{"model.gguf", []string{"Other"}, "model"},
		{"Qwen3.8-27B-GSQ-RCO-IQ2_S-mtp.gguf", []string{"Qwen3.8-27B"}, "GSQ-RCO-IQ2_S-mtp"},
	} {
		if got := Tag(c.file, c.names...); got != c.want {
			t.Errorf("Tag(%q) = %q, want %q", c.file, got, c.want)
		}
	}
	for name, want := range map[string]string{
		"mmproj-Qwen3.8-27B-BF16.gguf":       "mmproj",
		"MTP/mtp-Qwen3.8-27B-Q4_0.gguf":      "mtp",
		"dflash-Qwen3.8-27B-Q8_0.gguf":       "dflash",
		"eagle3-x.gguf":                      "eagle3",
		"imatrix-qwen3.8-27b.gguf":           "imatrix",
		"Qwen3.8-27B-GSQ-RCO-IQ2_S-mtp.gguf": "",
		"Qwen3.8-27B-Q4_K_M.gguf":            "",
	} {
		if got := Sidecar(name); got != want {
			t.Errorf("Sidecar(%q) = %q, want %q", name, got, want)
		}
	}
	if RepoBase("unsloth/Qwen3.8-27B-GGUF") != "Qwen3.8-27B" {
		t.Error("RepoBase")
	}
}
