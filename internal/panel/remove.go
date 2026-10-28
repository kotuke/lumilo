package panel

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/locales"
)

// removable is the installed model id, when the engine can delete it.
func (p *Panel) removable(id string) (engine.Model, bool) {
	if !p.view.CanRemove {
		return engine.Model{}, false
	}
	i := slices.IndexFunc(p.view.Models, func(m engine.Model) bool { return m.ID == id })
	if i < 0 {
		return engine.Model{}, false
	}
	return p.view.Models[i], true
}

// removeBlock is why the model id cannot be deleted now, "" when it can:
// the supervisor refuses it while another deletion, a download of the
// engine, an update, an install or a switch runs (core.RemoveModel), and
// for a model a server started elsewhere runs, which the model list never
// shows (modelCanOpen). m nil gives a non-empty reason without words.
func (p *Panel) removeBlock(m *locales.Messages, id string) string {
	v := p.view
	say := func(f func(locales.Messages) string) string {
		if m == nil {
			return "blocked"
		}
		return f(*m)
	}
	switch {
	case v.Removal.Running:
		return say(locales.Messages.RmBusy)
	case v.Switching || v.Update == core.UpdateUpdating || isInstallPhase(v.Phase):
		return say(func(m locales.Messages) string { return m.RmWait(v.Engine) })
	case slices.ContainsFunc(v.Downloads, func(j core.DownloadJob) bool {
		return j.State == core.DownloadRunning || j.State == core.DownloadCancelling || j.State == core.DownloadQueued
	}):
		return say(locales.Messages.RmWaitDownload)
	}
	return ""
}

// openRemove asks to delete the model: the dialog shows at once, and the
// size it frees once the engine counted it.
func (p *Panel) openRemove(id string) {
	p.removeID = id
	p.openDialog(dlgRemove)
	p.act.PlanRemove(id)
}

// closeRemove closes the dialog without deleting.
func (p *Panel) closeRemove() {
	if p.dlg == dlgRemove {
		p.dlg = dlgNone
	}
	if p.removeID != "" {
		p.removeID = ""
		p.act.CancelRemove()
	}
}

// removeTexts are the title and the note of the delete dialog; ready is
// set once the size is counted, which the Delete button waits for.
func (p *Panel) removeTexts(f *frame) (title, note string, ready bool) {
	m, v := f.m, p.view
	md, _ := p.removable(p.removeID)
	title = m.RmTitle(modelName(md))
	r := v.Removal
	switch {
	case r.Model != p.removeID || (!r.Counted && r.Error == ""):
		return title, m.RmCounting(), false
	case !r.Counted:
		return title, m.RmCountFailed(r.Error), false
	}
	notes := []string{m.RmFrees(sizeText(f.loc, r.Plan.Bytes))}
	if r.Plan.Shared {
		notes = append(notes, m.RmShared())
	}
	if p.running() && v.Model.ID == p.removeID {
		notes = append(notes, m.RmStops())
	}
	return title, strings.Join(notes, " "), true
}

// removeButton is the trash button of a model row: it asks to delete the
// model. While the model cannot be deleted it only shows, with the reason
// as its tooltip, as a draft the engine always runs does (featureToggle);
// its click never picks the row.
func (p *Panel) removeButton(c *ui.Context, f *frame, md engine.Model) {
	t, m := f.pal, f.m
	label := m.RmLabel(modelName(md))
	reason := p.removeBlock(&m, md.ID)
	b := ui.ButtonBase(c).Key("remove:"+md.ID).Size(22, 22).Radius(6).Justify(ui.Center).AlignItems(ui.Center).
		Shrink(0).Role(ui.RoleButton).Label(label)
	ink := t.muted
	if reason != "" {
		b.Cursor(ui.CursorDefault).Description(reason).Tooltip(reason)
		ink = t.muted.Alpha(featureOffOpacity)
	} else {
		id := md.ID
		b.Cursor(ui.CursorPointer).Tooltip(label).OnClick(func() { p.openRemove(id) })
		if b.Hovered() || b.FocusVisible() {
			b.Background(t.rowHover)
			ink = t.err
		}
	}
	b.TextColor(ink).Children(func() { ico(c, "trash", 2, 13) })
}

// modelName is the name a model shows with: its repository's, else its ID.
func modelName(md engine.Model) string {
	if md.Name != "" {
		return md.Name
	}
	return md.ID
}

// sizeText is a size on the disk: in megabytes below a gigabyte.
func sizeText(loc string, bytes int64) string {
	if bytes < 1_000_000_000 {
		return format.MB(loc, bytes)
	}
	return format.GB(loc, bytes)
}
