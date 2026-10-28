package format

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/kotuke/lumilo/locales"
)

// The mockup language codes and the catalog tags the app passes in.
var langTags = map[string]string{
	"ru": "ru", "en": "en", "de": "de", "zh": "zh-Hans",
	"ja": "ja", "fr": "fr", "es": "es", "ar": "ar",
}

var escapeRe = regexp.MustCompile(`\\u([0-9A-Fa-f]{4})`)

// decode turns the oracle's visible escapes (\u00A0) into the characters.
func decode(s string) string {
	return escapeRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.ParseUint(m[2:], 16, 32)
		return string(rune(n))
	})
}

func load(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

type oracle struct {
	NF map[string]struct {
		Values map[string]string `json:"values"`
	} `json:"nf"`
	Rendered map[string]map[string]string `json:"rendered"`
}

func gb(v float64) int64 { return int64(v * 1e9) }

// TestOracleNumbers checks Number against ICU's output for the 8 locales.
func TestOracleNumbers(t *testing.T) {
	var o oracle
	load(t, "format-oracle.json", &o)
	for lang, tag := range langTags {
		for in, want := range o.NF[lang].Values {
			v, _ := strconv.ParseFloat(in, 64)
			if got := Number(tag, v, 1); got != decode(want) {
				t.Errorf("Number(%s, %s, 1) = %q, want %q", tag, in, got, decode(want))
			}
		}
	}
}

// TestOracleRendered renders each full sentence of the mockup through the
// catalogs and the format helpers and compares it with the mockup's text.
func TestOracleRendered(t *testing.T) {
	var o oracle
	load(t, "format-oracle.json", &o)
	const engine = "Splash"
	for lang, tag := range langTags {
		m := locales.In(Tag(tag))
		first := func(d time.Duration) string { return m.FirstToken(Seconds(d)) }
		got := map[string]string{
			"memGenerating":        GB(tag, gb(21.4)),
			"memReady":             GB(tag, gb(17.2)),
			"memOf":                m.OfTotal(GB(tag, gb(38))),
			"modelSize":            "Mix-15.7G · " + GB(tag, gb(15.7)),
			"speedAvg":             TokPerSec(tag, 38),
			"diskText":             m.UsedOf(Number(tag, 6.4, 1), GB(tag, gb(32))),
			"diskTextReady":        m.UsedOf(Number(tag, 6.1, 1), GB(tag, gb(32))),
			"queue":                m.Queue(2),
			"foot1":                first(800 * time.Millisecond),
			"foot2":                m.PromptSpeed(TokPerSec(tag, 1240)),
			"hoverCurrent":         m.HoverCur() + " " + TokPerSec(tag, 41),
			"hoverRequest":         m.HoverReq() + " " + TokPerSec(tag, 39),
			"chartLabelGenerating": m.ChartLabel(20, Number(tag, 33, 1), TokPerSec(tag, 42)),
			"chartLabelReady":      m.ChartLabel(20, Number(tag, 31, 1), TokPerSec(tag, 42)),
			"tray_ready":           TrayGB(tag, gb(17.2)),
			"tray_generating":      TrayTPS(tag, 41),
			"availText":            m.Avail("1.4.1"),
			"updNoteText":          m.UpdNote(engine, "1.4.1"),
			"updFailText":          m.UpdFailTitle(engine, "1.4.1"),
			"keepOldText":          m.KeepOld("1.4.0"),
			"instTextFull":         m.InstText(engine, MB(tag, 270_000_000)),
			"openBrewText":         m.OpenBrew("brew.sh"),
			"brewReqText":          m.BrewReq("Apple silicon", "macOS 26.4"),
			"startOn":              m.ActStartOn(8001),
			// The mockup concatenates the label and the address, with no isolates.
			"copyAddrLabel": m.CopyAddr() + " 192.168.1.20:8000",
			"cut1":          m.CutRequests(1),
			"cut2":          m.CutRequests(2),
			"cut5":          m.CutRequests(5),
			"cut11":         m.CutRequests(11),
			"cut21":         m.CutRequests(21),
			"first0_8":      first(800 * time.Millisecond),
			"first1":        first(time.Second),
			"first2":        first(2 * time.Second),
			"first5":        first(5 * time.Second),
			"first21":       first(21 * time.Second),
			"first1_5":      first(1500 * time.Millisecond),
			"releasedFoot1": m.LastReq(Duration(tag, 18*time.Minute)),
		}
		for key, want := range o.Rendered[lang] {
			g, ok := got[key]
			if !ok {
				t.Errorf("%s: oracle key %q has no test", lang, key)
				continue
			}
			if g != decode(want) {
				t.Errorf("%s %s:\n got  %q\n want %q", lang, key, g, decode(want))
			}
		}
		if len(got) != len(o.Rendered[lang]) {
			t.Errorf("%s: %d tested sentences, oracle has %d", lang, len(got), len(o.Rendered[lang]))
		}
	}
}

// TestMockupTemplates checks the templates that had sample numbers baked in
// (sub.*, hangNote, idle, lastReq...) with the mockup's own sample values.
func TestMockupTemplates(t *testing.T) {
	var want map[string]map[string]string
	load(t, "mockup-templates.json", &want)
	const engine = "Splash"
	min := func(n int) time.Duration { return time.Duration(n) * time.Minute }
	for lang, tag := range langTags {
		m := locales.In(Tag(tag))
		d := func(h, mm, s int) string {
			return Duration(tag, time.Duration(h)*time.Hour+min(mm)+time.Duration(s)*time.Second)
		}
		got := map[string]string{
			"sub.loading":      m.SubLoading(d(0, 0, 24)),
			"sub.ready":        m.SubReady(d(2, 14, 0)),
			"sub.generating":   m.SubGenerating(d(2, 14, 0)),
			"sub.released":     m.SubReleased(d(3, 2, 0)),
			"sub.error":        m.SubError(d(0, 3, 0)),
			"sub.external":     m.SubExternal(d(2, 14, 0)),
			"sub.unresponsive": m.SubUnresponsive(d(0, 0, 45)),
			"errTitle.port":    m.ErrTitlePort(8000),
			"errTitle.memory":  m.ErrTitleMemory(engine, d(0, 10, 0)),
			"errTitle.exit":    m.ErrTitleExit(1),
			"hangNote":         m.HangNote(d(0, 0, 45)),
			"idle":             m.IdleNote(d(0, 7, 0)),
			"lastReq":          m.LastReq(d(0, 18, 0)),
			"settings.idle.5":  m.SettingsIdleAfter(Duration(tag, min(5))),
			"settings.idle.10": m.SettingsIdleAfter(Duration(tag, min(10))),
			"settings.idle.30": m.SettingsIdleAfter(Duration(tag, min(30))),
			"settings.idle.0":  m.SettingsIdleNever(),
			"hint.local":       m.HintLocal("127.0.0.1:8000"),
			"hint.network":     m.HintNetwork("192.168.1.20:8000"),
			"portBusy":         m.PortBusy(8000, 8001),
			"portTake":         m.PortTake(8001),
			"instSub":          m.SubInstalling(engine, "1.4.1"),
			"act.startOn":      m.ActStartOn(8001),
			"queue":            m.Queue(2),
			"avail":            m.Avail("1.4.1"),
			"keepOld":          m.KeepOld("1.4.0"),
		}
		for key, w := range want[lang] {
			g, ok := got[key]
			if !ok {
				t.Errorf("%s: key %q has no test", lang, key)
				continue
			}
			if g != w {
				t.Errorf("%s %s:\n got  %q\n want %q", lang, key, g, w)
			}
		}
	}
}

func TestDuration(t *testing.T) {
	sec := time.Second
	tests := []struct {
		loc  string
		d    time.Duration
		want string
	}{
		{"en", -5 * sec, "0 seconds"},
		{"en", 0, "0 seconds"},
		{"en", sec, "1 second"},
		{"en", 59*sec + 400*time.Millisecond, "59 seconds"},
		{"en", 59*sec + 600*time.Millisecond, "1 minute"},
		{"en", 61 * sec, "1 minute"},
		{"en", 3599 * sec, "59 minutes"},
		{"en", 3600 * sec, "1 hour"},
		{"en", 2*time.Hour + 14*time.Minute + 29*sec, "2 hours 14 minutes"},
		{"en", 2*time.Hour + 14*time.Minute + 31*sec, "2 hours 14 minutes"},
		{"en", 23*time.Hour + 59*time.Minute + 40*sec, "23 hours 59 minutes"},
		{"en", 23*time.Hour + 59*time.Minute + 59*sec + 600*time.Millisecond, "1 day"},
		{"en", 49*time.Hour + 10*time.Minute, "2 days 1 hour"},
		{"ru", 21 * sec, "21 секунду"},
		{"ru", 22 * time.Minute, "22 минуты"},
		{"ru", 25 * time.Hour, "1 день 1 час"},
		{"ru", 5*time.Hour + 11*time.Minute, "5 часов 11 минут"},
		{"de", 3 * 24 * time.Hour, "3 Tagen"},
		{"fr", 0, "0 seconde"},
		{"fr", 2*time.Hour + 14*time.Minute, "2 heures 14 minutes"},
		{"es", 2*time.Hour + 14*time.Minute, "2 horas y 14 minutos"},
		{"zh-Hans", 3*time.Hour + 2*time.Minute, "3 小时 2 分钟"},
		{"ja", 90 * time.Minute, "1 時間 30 分"},
		{"ar", 2*time.Hour + 14*time.Minute, "ساعتين و14 دقيقة"},
		{"ar", 3*time.Hour + 2*time.Minute, "3 ساعات ودقيقتين"},
		{"ar", sec, "ثانية واحدة"},
		{"ar", 100 * time.Minute, "ساعة واحدة و40 دقيقة"},
	}
	for _, tc := range tests {
		if got := Duration(tc.loc, tc.d); got != tc.want {
			t.Errorf("Duration(%s, %v) = %q, want %q", tc.loc, tc.d, got, tc.want)
		}
	}
}

// ShortDuration has the shapes of Duration with abbreviated units, joined
// by a space in every language, so the header subtitle stays on one line.
func TestShortDuration(t *testing.T) {
	sec := time.Second
	long := 26*time.Hour + 3*time.Minute
	tests := []struct {
		loc  string
		d    time.Duration
		want string
	}{
		{"en", -5 * sec, "0 s"},
		{"en", 24 * sec, "24 s"},
		{"en", 59*sec + 600*time.Millisecond, "1 min"},
		{"en", 18 * time.Minute, "18 min"},
		{"en", 3 * time.Hour, "3 h"},
		{"en", 2*time.Hour + 14*time.Minute, "2 h 14 min"},
		{"en", 49*time.Hour + 10*time.Minute, "2 d 1 h"},
		{"en", 72 * time.Hour, "3 d"},
		{"ru", 2*time.Hour + 14*time.Minute, "2 ч 14 мин"},
		{"ru", 25 * time.Hour, "1 дн 1 ч"},
		{"de", 2*time.Hour + 14*time.Minute, "2 Std. 14 Min."},
		{"de", long, "1 Tg. 2 Std."},
		{"fr", 2*time.Hour + 14*time.Minute, "2 h 14 min"},
		{"es", 2*time.Hour + 14*time.Minute, "2 h 14 min"},
		{"ja", 2*time.Hour + 14*time.Minute, "2 時間 14 分"},
		{"zh-Hans", 3*time.Hour + 2*time.Minute, "3 小时 2 分钟"},
		{"ar", 2*time.Hour + 14*time.Minute, "2 س 14 د"},
		{"ar", long, "1 ي 2 س"},
	}
	for _, tc := range tests {
		if got := ShortDuration(tc.loc, tc.d); got != tc.want {
			t.Errorf("ShortDuration(%s, %v) = %q, want %q", tc.loc, tc.d, got, tc.want)
		}
	}
}

func TestTrayRounding(t *testing.T) {
	tests := []struct {
		loc  string
		got  string
		want string
	}{
		{"en", TrayGB("en", gb(17.2)), "17 GB"},
		{"en", TrayGB("en", gb(17.5)), "18 GB"},
		{"en", TrayGB("en", gb(0.4)), "0 GB"},
		{"en", TrayGB("en", gb(37.6)), "38 GB"},
		{"ru", TrayGB("ru", gb(17.2)), "17 ГБ"},
		{"fr", TrayGB("fr", gb(17.2)), "17 Go"},
		{"ru", TrayTPS("ru", 40.6), "41 Т/С"},
		{"en", TrayTPS("en", 41), "41 T/S"},
		{"ar", TrayGB("ar", gb(17.2)), "17 GB"}, // the tray item is never isolated
		{"en", TrayTPS("en", 1240), "1,240 T/S"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.loc, tc.got, tc.want)
		}
	}
}

func TestNumberDecimals(t *testing.T) {
	tests := []struct {
		loc      string
		v        float64
		decimals int
		want     string
	}{
		{"en", 38, 1, "38"},
		{"en", 17.25, 1, "17.3"},
		{"en", 17.2, 0, "17"},
		{"en", 0.5, 0, "1"},
		{"en", 1234567.891, 2, "1,234,567.89"},
		{"es", 1240, 1, "1240"},
		{"es", 12400, 1, "12.400"},
		{"fr", 1240, 1, "1\u202f240"},
		{"ru", 1240, 1, "1\u00a0240"},
		{"de", 1240.5, 1, "1.240,5"},
		{"ar", 1240.5, 1, "1,240.5"},
	}
	for _, tc := range tests {
		if got := Number(tc.loc, tc.v, tc.decimals); got != tc.want {
			t.Errorf("Number(%s, %v, %d) = %q, want %q", tc.loc, tc.v, tc.decimals, got, tc.want)
		}
	}
}

func TestSizes(t *testing.T) {
	tests := []struct{ got, want string }{
		{GB("en", gb(17.2)), "17.2 GB"},
		{GB("ru", gb(17.2)), "17,2 ГБ"},
		{GB("fr", gb(17.2)), "17,2 Go"},
		{GB("en", 0), "0 GB"},
		{GB("en", gb(0.95)), "1 GB"},
		{MB("en", 270_000_000), "270 MB"},
		{MB("ru", 270_000_000), "270 МБ"},
		{MB("fr", 270_000_000), "270 Mo"},
		{TokPerSec("ru", 38), "38 т/с"},
		{TokPerSec("fr", 1240), "1\u202f240 tok/s"},
		{TokPerSec("ar", 38), "\u206838 tok/s\u2069"},
		{GB("ar", gb(17.2)), "\u206817.2 GB\u2069"},
		{MB("ar", 270_000_000), "\u2068270 MB\u2069"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestIsolate(t *testing.T) {
	if got := Isolate("ar", "1.4.1"); got != "\u20681.4.1\u2069" {
		t.Errorf("ar: %q", got)
	}
	if got := Isolate("ar", Isolate("ar", "1.4.1")); got != "\u20681.4.1\u2069" {
		t.Errorf("ar twice: %q", got)
	}
	if got := Isolate("ar-SA", "x"); got != "\u2068x\u2069" {
		t.Errorf("ar-SA: %q", got)
	}
	if got := Isolate("ar", ""); got != "" {
		t.Errorf("empty: %q", got)
	}
	for _, l := range []string{"en", "ru", "de", "zh-Hans", "ja", "fr", "es"} {
		if got := Isolate(l, "1.4.1"); got != "1.4.1" {
			t.Errorf("%s: %q", l, got)
		}
	}
}

func TestTag(t *testing.T) {
	tests := map[string]string{
		"ar": "ar-u-nu-latn", "ar-SA": "ar-SA-u-nu-latn", "ar-EG-u-nu-arab": "ar-EG-u-nu-arab",
		"ru": "ru", "zh-Hans": "zh-Hans", "en": "en",
	}
	for in, want := range tests {
		if got := Tag(in); got != want {
			t.Errorf("Tag(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestArabicLatinDigits asserts that no Arabic output has Arabic-Indic digits.
func TestArabicLatinDigits(t *testing.T) {
	m := locales.In(Tag("ar"))
	var out []string
	out = append(out,
		Number("ar", 1234567.891, 3), GB("ar", gb(17.2)), MB("ar", 270_000_000),
		TokPerSec("ar", 1240.5), TrayGB("ar", gb(17.2)), TrayTPS("ar", 41),
		Duration("ar", 26*time.Hour+3*time.Minute),
		m.Queue(1234), m.ActStartOn(8001), m.ErrTitlePort(8000), m.ErrTitleExit(137),
		m.PortBusy(8000, 8001), m.CutRequests(100), m.CutRequests(11),
		m.FirstToken(1.5), m.ChartLabel(20, "31", "42"), m.DurMinutes(1234),
		m.SettingsIdleAfter(Duration("ar", 10*time.Minute)),
	)
	for _, s := range out {
		for _, r := range s {
			if (r >= 0x0660 && r <= 0x0669) || (r >= 0x06F0 && r <= 0x06F9) {
				t.Errorf("Arabic-Indic digit %q in %q", r, s)
				break
			}
		}
	}
}

func TestSeconds(t *testing.T) {
	tests := map[time.Duration]float64{
		800 * time.Millisecond:  0.8,
		834 * time.Millisecond:  0.8,
		850 * time.Millisecond:  0.9,
		1000 * time.Millisecond: 1,
		1549 * time.Millisecond: 1.5,
		21 * time.Second:        21,
	}
	for d, want := range tests {
		if got := Seconds(d); got != want {
			t.Errorf("Seconds(%v) = %v, want %v", d, got, want)
		}
	}
}

func TestLanguageName(t *testing.T) {
	want := map[string]string{
		"ru": "Русский", "en": "English", "de": "Deutsch", "zh-Hans": "简体中文",
		"ja": "日本語", "fr": "Français", "es": "Español", "ar": "العربية",
	}
	for tag, w := range want {
		if got := LanguageName(tag); got != w {
			t.Errorf("LanguageName(%q) = %q, want %q", tag, got, w)
		}
	}
}

func TestRTL(t *testing.T) {
	for _, l := range []string{"ru", "en", "de", "zh-Hans", "ja", "fr", "es"} {
		if RTL(l) {
			t.Errorf("RTL(%q) = true", l)
		}
	}
	if !RTL("ar") || !RTL("ar-SA") {
		t.Error("ar is right to left")
	}
}
