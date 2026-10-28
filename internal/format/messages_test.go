package format

import (
	"strings"
	"testing"
)

// Messages formats the numbers of Arabic texts in Latin digits, as Number
// does: the panel, the tray menu and the tooltip read the same catalog.
func TestMessagesLatinDigits(t *testing.T) {
	if got := Messages("ar").ActStartOn(8001); !strings.Contains(got, "8001") {
		t.Errorf("ar: %q", got)
	}
	if got := Messages("ru").ActStartOn(8001); !strings.Contains(got, "8001") {
		t.Errorf("ru: %q", got)
	}
}
