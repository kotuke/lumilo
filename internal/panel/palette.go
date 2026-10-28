package panel

import "github.com/egoist/mygo/ui"

// palette holds the colour tokens of the approved mockup. Main and Settings
// use separate token sets: they differ in five values (miniEmpty, errBtn,
// warnText, startBg), so each screen asks for its own.
type palette struct {
	text, strong, muted, soft         ui.Color
	panel, border, btn                ui.Color
	card, cardBorder, chip            ui.Color
	pop, popBorder, popShadow         ui.Color
	rowHover, tint, segEmpty          ui.Color
	ok, okText, okBg                  ui.Color
	warn, warnText, warnBg, warnBtn   ui.Color
	okBar, warnBar, errBar, errLine   ui.Color
	err, errBg, errBtn, idleBg        ui.Color
	startText, startBg, outline       ui.Color
	fillBg, fillText, brewTile, focus ui.Color

	// Settings only.
	seg, segOn, segShadow, field, portBorder, dash ui.Color
	trackOn, trackOff, trackOffBorder, knob        ui.Color

	// Colours the mockup writes in its markup, not as tokens.
	barHi, barLo ui.Color // bright and dim chart bars; barHi also fills memory and disk
}

var homebrewInk = ui.Hex("#FBB040")

func paletteFor(dark, settings bool) palette {
	var p palette
	if dark {
		p = palette{
			text: ui.Hex("#E8EAED"), strong: ui.Hex("#F3F4F6"), muted: ui.Hex("#9CA3AF"), soft: ui.Hex("#C9CDD4"),
			panel: ui.Hex("#17191F"), border: ui.RGBA(255, 255, 255, 0.08), btn: ui.Hex("#1F222A"),
			card: ui.Hex("#1E2028"), cardBorder: ui.RGBA(255, 255, 255, 0.05), chip: ui.Hex("#2C2F3A"),
			pop: ui.Hex("#2A2D37"), popBorder: ui.RGBA(255, 255, 255, 0.1), popShadow: ui.RGBA(0, 0, 0, 0.5),
			rowHover: ui.RGBA(255, 255, 255, 0.06), tint: ui.RGBA(255, 255, 255, 0.08), segEmpty: ui.RGBA(255, 255, 255, 0.10),
			ok: ui.Hex("#6CBF84"), okText: ui.Hex("#8FD3A3"), okBg: ui.RGBA(108, 191, 132, 0.14),
			warn: ui.Hex("#E0B450"), warnText: ui.Hex("#F0CB75"), warnBg: ui.RGBA(224, 180, 80, 0.14), warnBtn: ui.RGBA(224, 180, 80, 0.22),
			okBar: ui.Hex("#6CBF84"), warnBar: ui.Hex("#E0B450"), errBar: ui.Hex("#E06C60"), errLine: ui.RGBA(240, 140, 130, 0.7),
			err: ui.Hex("#F08C82"), errBg: ui.RGBA(224, 108, 96, 0.12), errBtn: ui.RGBA(224, 108, 96, 0.14), idleBg: ui.RGBA(255, 255, 255, 0.08),
			startText: ui.Hex("#0B1220"), startBg: ui.Hex("#6CBF84"), outline: ui.RGBA(255, 255, 255, 0.35),
			fillBg: ui.Hex("#F3F4F6"), fillText: ui.Hex("#17191F"), brewTile: ui.Hex("#1D1D1F"), focus: ui.Hex("#0A84FF"),

			seg: ui.Hex("#14161C"), segOn: ui.Hex("#2C2F3A"), segShadow: ui.RGBA(255, 255, 255, 0.12),
			field: ui.Hex("#14161C"), portBorder: ui.RGBA(255, 255, 255, 0.35), dash: ui.RGBA(255, 255, 255, 0.35),
			trackOn: ui.Hex("#4E9A66"), trackOff: ui.Hex("#5B6170"),
			trackOffBorder: ui.RGBA(255, 255, 255, 0.35), knob: ui.Hex("#F3F4F6"),

			barHi: ui.RGBA(243, 244, 246, 0.6), barLo: ui.RGBA(255, 255, 255, 0.14),
		}
		if settings {
			p.errBtn = ui.RGBA(224, 108, 96, 0.2)
		}
		return p
	}
	p = palette{
		text: ui.Hex("#1D1D1F"), strong: ui.Hex("#1D1D1F"), muted: ui.Hex("#5F636B"), soft: ui.Hex("#3F434B"),
		panel: ui.Hex("#ECEEF2"), border: ui.RGBA(0, 0, 0, 0.08), btn: ui.Hex("#F5F6F8"),
		card: ui.Hex("#F5F6F8"), cardBorder: ui.RGBA(0, 0, 0, 0.05), chip: ui.Hex("#E3E5EA"),
		pop: ui.Hex("#FFFFFF"), popBorder: ui.RGBA(0, 0, 0, 0.1), popShadow: ui.RGBA(0, 0, 0, 0.16),
		rowHover: ui.RGBA(0, 0, 0, 0.05), tint: ui.RGBA(0, 0, 0, 0.06), segEmpty: ui.RGBA(0, 0, 0, 0.08),
		ok: ui.Hex("#5AA86E"), okText: ui.Hex("#2F6B3F"), okBg: ui.RGBA(90, 168, 110, 0.16),
		warn: ui.Hex("#D99A3C"), warnText: ui.Hex("#7A4E0D"), warnBg: ui.RGBA(217, 154, 60, 0.16), warnBtn: ui.RGBA(217, 154, 60, 0.24),
		okBar: ui.Hex("#3F8A55"), warnBar: ui.Hex("#A8680F"), errBar: ui.Hex("#C0443A"), errLine: ui.RGBA(165, 47, 38, 0.7),
		err: ui.Hex("#A52F26"), errBg: ui.RGBA(207, 85, 74, 0.12), errBtn: ui.RGBA(207, 85, 74, 0.10), idleBg: ui.RGBA(0, 0, 0, 0.06),
		startText: ui.Hex("#FFFFFF"), startBg: ui.Hex("#2F7D4A"), outline: ui.RGBA(0, 0, 0, 0.45),
		fillBg: ui.Hex("#1D1D1F"), fillText: ui.Hex("#F5F6F8"), brewTile: ui.Hex("#1D1D1F"), focus: ui.Hex("#0A60D0"),

		seg: ui.Hex("#E3E5EA"), segOn: ui.Hex("#FFFFFF"), segShadow: ui.RGBA(0, 0, 0, 0.12),
		field: ui.Hex("#FFFFFF"), portBorder: ui.RGBA(0, 0, 0, 0.45), dash: ui.RGBA(0, 0, 0, 0.45),
		trackOn: ui.Hex("#3F8A55"), trackOff: ui.Hex("#B8BCC6"),
		trackOffBorder: ui.RGBA(0, 0, 0, 0.25), knob: ui.Hex("#FFFFFF"),

		// The older bars keep 3:1 against the card (WCAG 1.4.11); the last
		// ten stay darker.
		barHi: ui.RGBA(29, 29, 31, 0.72), barLo: ui.RGBA(0, 0, 0, 0.44),
	}
	if settings {
		p.warnText = ui.Hex("#8A5A12")
		p.startBg = ui.Hex("#3F8A55")
	}
	return p
}
