// Package format writes the numbers, sizes, speeds and durations the panel
// shows, in the language the UI shows.
//
// Every function takes a locale tag as mygo.App.Locale returns it ("ru",
// "zh-Hans", "ar"). Units and duration words come from the catalogs in
// package locales, so a unit is translated in one place only.
//
// Two rules hold for every function:
//
//   - Arabic writes Latin digits. Tag adds the "latn" numbering system to an
//     Arabic tag, and all functions format with that tag.
//   - Text that mixes Latin numbers with units inside right-to-left text is
//     isolated (U+2068 ... U+2069). GB, MB and TokPerSec return isolated text
//     for Arabic on their own, because their catalog messages carry the
//     isolates. Do not wrap their results again.
package format

import (
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/i18n"

	"github.com/kotuke/lumilo/locales"
)

const (
	// FSI is U+2068 FIRST STRONG ISOLATE. The panel uses it, not U+2066.
	FSI = "⁨"
	// PDI is U+2069 POP DIRECTIONAL ISOLATE.
	PDI = "⁩"
)

const bytesPerGB = 1e9 // decimal gigabytes, as the mockup shows
const bytesPerMB = 1e6

// Tag returns the locale tag used for formatting and catalog lookup.
// Arabic gets "-u-nu-latn" (Latin digits); every other tag is unchanged.
// A tag that already has a Unicode extension is returned as it is.
func Tag(loc string) string {
	if i18n.Base(loc) == "ar" && !strings.Contains(loc, "-u-") {
		return loc + "-u-nu-latn"
	}
	return loc
}

// Messages is the app's catalog in the language loc. Every text of the
// panel, the tray menu and the tooltip comes through it, so all of them
// format numbers the same way (Tag).
func Messages(loc string) locales.Messages { return locales.In(Tag(loc)) }

// Number writes v with at most decimals fraction digits, rounded half away
// from zero, in the locale's separators and grouping: 17.2 is "17,2" in
// Russian, 1240 is "1,240" in English and "1 240" (U+00A0) in Russian.
// Zero decimals writes a whole number.
func Number(loc string, v float64, decimals int) string {
	o := i18n.NumberOptions{MaxFractionDigits: decimals}
	if decimals <= 0 {
		o = i18n.NumberOptions{Integer: true}
	}
	return i18n.FormatNumber(Tag(loc), v, o)
}

// GB writes a size in decimal gigabytes with at most one fraction digit:
// "17.2 GB", "17,2 ГБ", "17,2 Go". Arabic is isolated.
func GB(loc string, bytes int64) string {
	return Messages(loc).SizeGb(Number(loc, float64(bytes)/bytesPerGB, 1))
}

// MB writes a size in decimal megabytes with at most one fraction digit:
// "270 MB", "270 МБ", "270 Mo". Arabic is isolated.
func MB(loc string, bytes int64) string {
	return Messages(loc).SizeMb(Number(loc, float64(bytes)/bytesPerMB, 1))
}

// TokPerSec writes a speed with at most one fraction digit: "38 tok/s",
// "38 т/с", "1,240 tok/s". Arabic is isolated.
func TokPerSec(loc string, v float64) string {
	return Messages(loc).SpeedTps(Number(loc, v, 1))
}

// TrayGB writes the menu bar memory value: a whole number of decimal
// gigabytes (rounded half away from zero) and the unit, "17 GB", "17 ГБ",
// "17 Go". It is never isolated, because the tray item reads left to right.
func TrayGB(loc string, bytes int64) string {
	return Messages(loc).TrayGb(Number(loc, float64(bytes)/bytesPerGB, 0))
}

// TrayTPS writes the menu bar speed: a whole number and the upper-case
// unit, "41 T/S", "41 Т/С". It is never isolated.
func TrayTPS(loc string, v float64) string {
	return Messages(loc).TrayTps(Number(loc, v, 0))
}

// Duration writes how long something lasted in the words of the locale,
// built from the plural messages of the catalog.
//
// The duration is rounded to the nearest whole second (half up), then
// split by the largest units and truncated, with these shapes:
//
//	under 1 minute   seconds             "24 seconds"
//	under 1 hour     minutes             "18 minutes"
//	under 1 day      hours and minutes   "2 hours 14 minutes", "3 hours"
//	1 day or more    days and hours      "2 days 3 hours", "1 day"
//
// A part that is zero is left out, except for 0 seconds. A negative
// duration is written as zero. The words are in the form that follows a
// preposition or a verb ("in 7 minutes", "ago", "for"), which is the form
// every panel template uses.
func Duration(loc string, d time.Duration) string {
	m := Messages(loc)
	return duration(d, m.DurSeconds, m.DurMinutes, m.DurHours, m.DurDays, m.DurJoin)
}

// ShortDuration is Duration with abbreviated units and no linking word, for
// the header subtitle, which must stay on one line: "24 s", "18 min",
// "2 h 14 min", "2 d 3 h" in English, "2 ч 14 мин" in Russian. The parts
// are the same as Duration's. The units are not in the form that follows a
// preposition, so they need no case.
func ShortDuration(loc string, d time.Duration) string {
	m := Messages(loc)
	return duration(d, m.DurShortSeconds, m.DurShortMinutes, m.DurShortHours, m.DurShortDays,
		func(a, b string) string { return a + " " + b })
}

// duration splits d as Duration says and writes the parts with the given
// unit messages.
func duration(d time.Duration, secs, mins, hours, days func(int) string, join func(a, b string) string) string {
	total := int64(math.Floor(d.Seconds() + 0.5))
	if total < 0 {
		total = 0
	}
	const minute, hour, day = 60, 3600, 86400
	switch {
	case total < minute:
		return secs(int(total))
	case total < hour:
		return mins(int(total / minute))
	case total < day:
		h, m := int(total/hour), int(total%hour/minute)
		if m == 0 {
			return hours(h)
		}
		return join(hours(h), mins(m))
	default:
		dd, h := int(total/day), int(total%day/hour)
		if h == 0 {
			return days(dd)
		}
		return join(days(dd), hours(h))
	}
}

// Seconds returns d in seconds rounded to one decimal place. Pass it to
// locales.FirstToken, whose Russian plural reads the number as it is
// written: 0.8 is "0,8 секунды", 1 is "1 секунду".
func Seconds(d time.Duration) float64 {
	return math.Round(d.Seconds()*10) / 10
}

// Isolate wraps s in U+2068 ... U+2069 for Arabic and returns it unchanged
// for every other language. Use it for Latin text (a number with a unit, a
// version, a host name, an address) that you put in Arabic text yourself.
// Arguments of catalog messages need no Isolate: the Arabic messages
// isolate them. Text that is already isolated is returned as it is.
func Isolate(loc, s string) string {
	if i18n.Base(loc) != "ar" || s == "" {
		return s
	}
	if strings.HasPrefix(s, FSI) && strings.HasSuffix(s, PDI) {
		return s
	}
	return FSI + s + PDI
}

// RTL reports whether the language is laid out right to left in the panel.
// Only Arabic ships, so only Arabic is true.
func RTL(loc string) bool { return i18n.Base(loc) == "ar" }

// LanguageName returns the name of a language in itself for the language
// list of Settings, with the first letter in upper case. CLDR writes
// "русский", "français" and "español" in lower case, and "简体中文" for
// zh-Hans; the panel shows "Русский", "Français", "Español" and "简体中文".
func LanguageName(tag string) string {
	name := i18n.Autonym(tag)
	r, size := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError || !unicode.IsLower(r) {
		return name
	}
	return string(unicode.ToUpper(r)) + name[size:]
}
