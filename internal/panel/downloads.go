package panel

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
)

// A model downloaded apart from the server (core.View.Downloads) shows in
// the model list, or among the suggestions while there is no model, as a
// row of its own: the progress bar and Pause and Cancel buttons while it
// fetches, "Queued" with Pause and Cancel while it waits for the one before
// it, "Paused" with what it fetched and Resume and Remove buttons of the
// same kind after a Pause, a quit of Lumilo or a change of engine, the
// reason and Retry and Remove under it once it failed. It cannot be
// picked until it is done; then it is an ordinary model row with a
// "Downloaded" tag.

// runningJob is the download job that fetches files now, if any.
func runningJob(v core.View) (core.DownloadJob, bool) {
	for _, j := range v.Downloads {
		if j.State == core.DownloadRunning {
			return j, true
		}
	}
	return core.DownloadJob{}, false
}

// jobFor is the job of the model id that is not done yet.
func jobFor(v core.View, id string) (core.DownloadJob, bool) {
	for _, j := range v.Downloads {
		if j.Model.ID == id && j.State != core.DownloadDone {
			return j, true
		}
	}
	return core.DownloadJob{}, false
}

// downloaded reports whether the model id came from a job that finished
// while the panel watched (its tag).
func downloaded(v core.View, id string) bool {
	for _, j := range v.Downloads {
		if j.State == core.DownloadDone && (j.Installed == id || j.Model.ID == id) {
			return true
		}
	}
	return false
}

// jobTotal is the size of a job's download: what the engine said, else the
// variant's size the field knew, else the suggestion's; 0 = not known.
func (p *Panel) jobTotal(j core.DownloadJob) int64 {
	if j.Total > 0 {
		return j.Total
	}
	if n := p.dlTotal[j.Model.ID]; n > 0 {
		return n
	}
	for _, sg := range p.view.Suggestions {
		if sg.ID == j.Model.ID {
			return sg.DownloadBytes
		}
	}
	return 0
}

// jobName is what a job's row calls the model: the suggestion's name, else
// the repository's.
func (p *Panel) jobName(j core.DownloadJob) string {
	for _, sg := range p.view.Suggestions {
		if sg.ID == j.Model.ID {
			return sg.Name
		}
	}
	if j.Model.Name != "" {
		return j.Model.Name
	}
	return j.Model.ID
}

// jobReason is why a job failed, in the user's words, and whether it is
// the model's image input (then Retry downloads it for text only).
func jobReason(f *frame, j core.DownloadJob) (text string, noVision bool) {
	m := f.m
	switch j.Failure.Kind {
	case engine.FailNoVision:
		return m.DlFailNoVision(), true
	case engine.FailModel:
		return m.DlFailModel(), false
	}
	return m.DlFailed(), false
}

// jobRow is the row of a download job that is not done. logo is the
// maker's logo of a suggested model, nil for none; inset pads the row as
// the model list's rows, while among the suggestions it lines up with them.
func (p *Panel) jobRow(c *ui.Context, f *frame, j core.DownloadJob, logo []byte, inset bool) {
	t, m := f.pal, f.m
	id, opts := j.Model.ID, j.Options
	name := p.jobName(j)
	row := ui.Row(c).Key("job-" + id).Gap(10).PaddingY(6).Radius(8).AlignItems(ui.Center)
	if inset {
		row.PaddingX(8)
	}
	row.Children(func() {
		if src := f.brands.image(logo, engine.LogoSVG); src != nil {
			ui.Image(c, src).Size(22, 22).Shrink(0).AlignSelf(ui.Start).Margin(2, 0, 0, 0)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Column(c).Gap(1).Children(func() {
				txt(c, name, 12.5).FontWeight(600).SingleLine().Direction(ui.LeftToRight).AlignSelf(ui.Start).TextColor(t.soft)
				switch j.State {
				case core.DownloadRunning:
					total := p.jobTotal(j)
					text, left := m.DlPreparing(), ""
					if total > 0 || j.Done > 0 {
						text, left = p.progressText(f, total, j.Done)
					}
					progressLine(c, f, text, left, 11)
				case core.DownloadCancelling:
					txt(c, m.DlCancelling(), 11).TextColor(t.muted).Role(ui.RoleStatus)
				case core.DownloadQueued:
					txt(c, m.DlQueued(), 11).TextColor(t.muted).Role(ui.RoleStatus)
				case core.DownloadPaused:
					text := m.DlPaused()
					if total := p.jobTotal(j); total > 0 && j.Done > 0 && j.Done < total {
						text += " · " + m.UsedOf(format.Number(f.loc, float64(j.Done)/1e9, 1), format.GB(f.loc, total))
					}
					txt(c, text, 11).TextColor(t.muted).SingleLine().Role(ui.RoleStatus)
				case core.DownloadFailed:
					reason, _ := jobReason(f, j)
					e := txt(c, reason, 11).TextColor(t.err).LineHeight(1.35).Role(ui.RoleStatus)
					if line := j.Failure.Line; line != "" {
						e.Tooltip(line).Description(line)
						if j.Failure.Kind != engine.FailNoVision {
							// Ours is the kind; the engine's own line says
							// what happened (no network, a gated repository).
							monoTxt(c, line, 11).TextColor(t.muted).SingleLine().Direction(ui.LeftToRight).
								AlignSelf(ui.Start).Tooltip(line)
						}
					}
				}
			})
			switch j.State {
			case core.DownloadRunning:
				p.jobBar(c, f, j)
			case core.DownloadFailed:
				p.jobActions(c, f, j, id, opts)
			}
		})
		switch j.State {
		case core.DownloadRunning, core.DownloadQueued:
			jobButton(c, f, "job-pause-"+id, "player-pause", m.DlPause(), name, func() { p.act.PauseDownload(id) })
			jobButton(c, f, "job-cancel-"+id, "x", m.DlCancel(), name, func() { p.act.CancelDownload(id) })
		case core.DownloadPaused:
			resume := func() { p.act.Download(id, opts) }
			if p.downloadBlocked() {
				resume = nil
			}
			jobButton(c, f, "job-retry-"+id, "player-play", m.DlResume(), name, resume)
			jobButton(c, f, "job-remove-"+id, "x", m.DlRemove(), name, func() { p.act.DismissDownload(id) })
		}
	})
}

// jobButton is a small icon button of a job's row: label names what it
// does, and with the model's name it is what a screen reader says. A nil
// click shows it disabled (a download cannot start now).
func jobButton(c *ui.Context, f *frame, key, icon, label, name string, click func()) {
	t := f.pal
	x := btn(c).Key(key).Size(22, 22).Radius(6).Shrink(0).TextColor(t.muted).
		Label(label + " " + name).Tooltip(label)
	if click == nil {
		x.Disabled(true).Cursor(ui.CursorDefault).Opacity(0.6)
	} else {
		if x.Hovered() || x.FocusVisible() {
			x.Background(t.rowHover).TextColor(t.text)
		}
		x.OnClick(click)
	}
	x.Children(func() { ico(c, icon, 2.2, 14) })
}

// jobBar is the progress bar of a running job: filled up to the fetched
// part once its size and first bytes are known, else the sweep.
func (p *Panel) jobBar(c *ui.Context, f *frame, j core.DownloadJob) {
	t := f.pal
	total := p.jobTotal(j)
	label := p.jobName(j)
	if total <= 0 || j.Done <= 0 {
		sweep(c, f, label).Height(6)
		return
	}
	pct := min(float64(j.Done)/float64(total)*100, 100)
	bar := segments(c, f, 6, 2, func(i int) ui.Color {
		if filled(i, 30, pct) {
			return t.warnBar
		}
		return t.segEmpty
	})
	bar.Role(ui.RoleProgress).Label(label).Range(0, float64(total), float64(min(j.Done, total)))
}

// jobActions are the buttons of a failed job: Retry, or download for text
// only when the model's image input failed; and Remove.
func (p *Panel) jobActions(c *ui.Context, f *frame, j core.DownloadJob, id string, opts engine.ModelOptions) {
	t, m := f.pal, f.m
	retry, icon := m.ActRetry(), "refresh"
	if _, noVision := jobReason(f, j); noVision && j.State == core.DownloadFailed {
		retry = m.DlTextOnly()
		opts.NoVision = true
	}
	blocked := p.downloadBlocked()
	ui.Row(c).Gap(6).Children(func() {
		b := btn(c).Key("job-retry-" + id).Height(22).Radius(11).PaddingX(8).Gap(5).Background(t.chip).TextColor(t.text)
		if blocked {
			b.Disabled(true).Cursor(ui.CursorDefault).Opacity(0.6)
		} else {
			b.OnClick(func() { p.act.Download(id, opts) })
		}
		b.Children(func() {
			ico(c, icon, 2, 12)
			txt(c, retry, 12).FontWeight(500).NoWrap()
		})
		btn(c).Key("job-remove-"+id).Height(22).Radius(11).PaddingX(8).Border(1, t.border).TextColor(t.soft).
			OnClick(func() { p.act.DismissDownload(id) }).Children(func() {
			txt(c, m.DlRemove(), 12).FontWeight(500).NoWrap()
		})
	})
}

// downloadedTag is the small "Downloaded" mark of a model row.
func downloadedTag(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	ui.Row(c).Gap(3).AlignItems(ui.Center).TextColor(t.ok).Shrink(0).Children(func() {
		ico(c, "circle-check", 2, 11)
		txt(c, m.Downloaded(), 11).NoWrap()
	})
}

// adoptable are the models on disk the engine can install from those
// files (core.View.Adoptable), less those already on their way.
func adoptable(v core.View) []engine.Model {
	var out []engine.Model
	for _, md := range v.Adoptable {
		if j, ok := jobFor(v, md.ID); ok && j.State != core.DownloadDone {
			continue
		}
		out = append(out, md)
	}
	return out
}

// adoptList is "Already on this Mac": the models the engine can install
// from files on disk, each with an Add button that downloads it, which
// takes only what is missing.
func (p *Panel) adoptList(c *ui.Context, f *frame, ms []engine.Model, inPopup bool) {
	t, m, v := f.pal, f.m, p.view
	if len(ms) == 0 {
		return
	}
	head := txt(c, m.AdoptTitle(), 11).TextColor(t.muted).SingleLine().Tooltip(m.AdoptNote(v.Engine))
	if inPopup {
		head.Padding(8, 8, 2, 8)
	} else {
		head.Padding(6, 0, 2, 0)
	}
	for _, md := range ms {
		sub := []string{md.Owner, md.Variant, format.GB(f.loc, md.SizeBytes)}
		row := ui.Row(c).Key("adopt-" + md.ID).Gap(10).MinHeight(40).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				txt(c, modelName(md), 12.5).FontWeight(600).SingleLine().Direction(ui.LeftToRight).AlignSelf(ui.Start)
				txt(c, strings.Join(sub, " · "), 11).TextColor(t.muted).SingleLine().Direction(ui.LeftToRight).AlignSelf(ui.Start)
			})
			id := md.ID
			b := btn(c).Key("adopt-btn-" + id).Height(24).Radius(12).PaddingStart(7).PaddingEnd(9).Gap(4).Shrink(0).
				Background(t.chip).TextColor(t.text).Label(m.Adopt() + " " + modelName(md))
			if reason := p.downloadBlock(&m); reason != "" {
				b.Disabled(true).Cursor(ui.CursorDefault).Opacity(0.6).Tooltip(reason).Description(reason)
			} else {
				b.Tooltip(m.AdoptNote(v.Engine)).OnClick(func() { p.act.Download(id, engine.ModelOptions{}) })
			}
			b.Children(func() {
				ico(c, "plus", 2.2, 12)
				txt(c, m.Adopt(), 12).FontWeight(600).NoWrap()
			})
		})
		if inPopup {
			row.PaddingX(8)
		}
	}
}
