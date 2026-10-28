package panel

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/kotuke/lumilo/internal/core"
	"github.com/kotuke/lumilo/internal/engine"
	"github.com/kotuke/lumilo/internal/format"
	"github.com/kotuke/lumilo/internal/hf"
	"github.com/kotuke/lumilo/locales"
)

// The name typed in "Add model…" completes in two steps. From two letters on,
// the repositories that match come under the field (SearchModels, once the
// typing has paused). A name with ":" lists the variants of its repository
// (ModelVariants), the 4-bit ones first, with their sizes.

const (
	// searchDelay is how long the typing pauses before the search is sent.
	searchDelay = 300 * time.Millisecond
	// searchMin is the shortest text that is searched, in letters.
	searchMin = 2
	// hitRows is as many repositories as SearchModels finds.
	hitRows = 8
	// repoRowH is the height of a repository's row.
	repoRowH = 38
	// addListAbove is about what lies above the list in the window: the
	// header, the server card's top and the model list's rows, the field.
	// The list scrolls only where the window, held to the screen
	// (SetMaxHeight), has no room for it under that.
	addListAbove = 300
	// addFieldGap is the space between the field, its list and its note.
	addFieldGap = 6
	// The field's padding in the model list, above and below.
	addPopPadTop, addPopPadBottom = 6, 2
)

// addState is the completion of the name being typed. It is used only on
// the UI thread.
type addState struct {
	hits  []hf.Repo
	asked string // the text last sent to SearchModels; hits answer it
	// answered is the text hits answer once its answer came: "" while the
	// search runs. failed marks an answer that was an error: Hugging Face
	// could not be reached.
	answered string
	failed   bool
	due      time.Time
	dueText  string // the text to send at due; zero due = nothing waits

	vars      []hf.Variant
	info      hf.RepoInfo // what the hub said of varsFor
	full      bool        // info is the hub's whole answer (SetModelInfo), not variants alone
	varsAsked string      // the repository last sent to ModelVariants
	varsFor   string      // the repository vars answer; "" until it does
	varsErr   bool        // varsFor's answer was an error: the hub could not be reached

	seen string // the text of the last frame
	skip string // the text a pick made: nothing to complete for it

	sel      int  // the highlighted row; -1 = none
	shut     bool // Esc closed the list; typing opens it again
	focus    bool // give the field the focus and the caret at the end
	showing  bool // the last frame drew the list
	rows     int  // how many rows the last frame drew
	loading  bool // the variants of the typed repository are on their way
	summary  bool // the last frame drew the summary of the chosen model
	hint     bool // the last frame drew the hint at what to type
	scrolled int  // the row last scrolled into view
}

// reset forgets everything and drops the lookups in flight.
func (st *addState) reset(act Actions) {
	busy := st.asked != "" || st.varsAsked != ""
	*st = addState{sel: -1, scrolled: -1}
	if busy {
		act.CancelModelSearch()
	}
}

// SetModelHits shows the repositories that answer a search. The answer of a
// search that a newer one replaced is dropped. An empty list shows that
// nothing was found.
func (p *Panel) SetModelHits(text string, hits []hf.Repo) {
	if text != p.add.asked {
		return
	}
	p.add.hits, p.add.answered, p.add.failed = append([]hf.Repo(nil), hits...), text, false
	p.gen++
}

// SetModelSearchFailed answers a search that could not reach Hugging Face:
// the field says so, instead of nothing found.
func (p *Panel) SetModelSearchFailed(text string) {
	if text != p.add.asked {
		return
	}
	p.add.hits, p.add.answered, p.add.failed = nil, text, true
	p.gen++
}

// SetModelVariants shows the variants of a GGUF repository, 4-bit first. An
// empty list, as a failed lookup gives, shows nothing.
// Nothing else of the repository is known then: whether it has a vision
// projector, say.
func (p *Panel) SetModelVariants(repo string, vs []hf.Variant) {
	p.setInfo(repo, hf.RepoInfo{ID: repo, GGUF: len(vs) > 0, Variants: vs}, false)
}

// SetModelInfo shows what the hub said of a repository: the variants the
// engine runs, with what each needs in memory, or whether the engine runs
// its MLX checkpoint.
func (p *Panel) SetModelInfo(repo string, info hf.RepoInfo) { p.setInfo(repo, info, true) }

// SetModelInfoFailed answers a look at a repository that could not reach
// Hugging Face.
func (p *Panel) SetModelInfoFailed(repo string) {
	p.setInfo(repo, hf.RepoInfo{ID: repo}, true)
	if strings.EqualFold(repo, p.add.varsFor) {
		p.add.varsErr = true
	}
}

func (p *Panel) setInfo(repo string, info hf.RepoInfo, full bool) {
	if !strings.EqualFold(repo, p.add.varsAsked) {
		return
	}
	info.Variants = sortVariants(info.Variants)
	p.add.vars, p.add.info, p.add.varsFor, p.add.full = info.Variants, info, p.add.varsAsked, full
	p.add.varsErr = false
	p.gen++
}

// sortVariants orders a copy of vs as the list shows them: the hub's
// first, the one it suggests (Q4_K_M), stays first, where Tab takes it;
// the others go by quant family (hf.QuantOf) in the hub's order, the
// other 4-bit ones next, and within a family from the smallest up.
func sortVariants(vs []hf.Variant) []hf.Variant {
	out := sortFamilies(vs)
	if len(vs) > 1 {
		i := slices.Index(out, vs[0])
		out = append([]hf.Variant{vs[0]}, slices.Delete(out, i, i+1)...)
	}
	return out
}

func sortFamilies(vs []hf.Variant) []hf.Variant {
	out := slices.Clone(vs)
	family := map[string]int{}
	key := func(v hf.Variant) string {
		if q := hf.QuantOf(v.Tag); q != "" {
			return q
		}
		return v.Tag
	}
	for _, v := range vs {
		if _, ok := family[key(v)]; !ok {
			family[key(v)] = len(family)
		}
	}
	slices.SortStableFunc(out, func(a, b hf.Variant) int {
		if fa, fb := family[key(a)], family[key(b)]; fa != fb {
			return fa - fb
		}
		return cmp.Compare(a.Bytes, b.Bytes)
	})
	return out
}

// addRow is a line of the list: a repository or a variant.
type addRow struct {
	repo    hf.Repo
	variant hf.Variant
	isVar   bool
}

// splitName cuts a typed name at ":" into the repository and what follows.
// repo is "" unless it has the form owner/repo.
func splitName(text string) (repo, tag string, colon bool) {
	r, tag, colon := strings.Cut(strings.TrimSpace(text), ":")
	owner, name, ok := strings.Cut(r, "/")
	if !ok || owner == "" || name == "" || strings.ContainsAny(r, " \t") || strings.Contains(name, "/") {
		return "", tag, colon
	}
	return r, tag, colon
}

// rowsFor are the lines to show for the text now typed.
func (st *addState) rowsFor(text string) []addRow {
	if st.shut || strings.TrimSpace(text) == st.skip && st.skip != "" {
		return nil
	}
	repo, tag, colon := splitName(text)
	var rows []addRow
	switch {
	case colon:
		if repo == "" || !strings.EqualFold(repo, st.varsFor) {
			return nil
		}
		t := strings.ToLower(strings.TrimSpace(tag))
		// A complete tag is a choice, not a search: no list, the summary.
		if t != "" && slices.ContainsFunc(st.vars, func(v hf.Variant) bool { return strings.EqualFold(v.Tag, t) }) {
			return nil
		}
		var rest []addRow
		for _, v := range st.vars {
			switch l := strings.ToLower(v.Tag); {
			case t == "" || strings.HasPrefix(l, t):
				rows = append(rows, addRow{variant: v, isVar: true})
			case strings.Contains(l, t):
				rest = append(rest, addRow{variant: v, isVar: true})
			}
		}
		rows = append(rows, rest...)
	case len([]rune(strings.TrimSpace(text))) >= searchMin:
		for _, h := range st.hits {
			rows = append(rows, addRow{repo: h})
		}
	}
	return rows
}

// typed notes that the text changed: it asks for the variants of a typed
// repository at once and for the search once the typing has paused.
func (st *addState) typed(c *ui.Context, act Actions, text string) {
	st.seen, st.sel, st.shut = text, -1, false
	trimmed := strings.TrimSpace(text)
	if trimmed == st.skip && trimmed != "" {
		st.due = time.Time{}
		return
	}
	st.skip = ""
	repo, _, colon := splitName(text)
	switch {
	case colon:
		st.due = time.Time{}
		if repo != "" && !strings.EqualFold(repo, st.varsAsked) {
			st.varsAsked, st.vars, st.varsFor, st.varsErr = repo, nil, "", false
			act.ModelVariants(repo)
		}
	case len([]rune(trimmed)) >= searchMin:
		st.due, st.dueText = c.Now().Add(searchDelay), trimmed
	default:
		st.due = time.Time{}
		if st.asked != "" {
			st.asked, st.hits, st.answered, st.failed = "", nil, "", false
			act.CancelModelSearch()
		}
	}
}

// searchAnswered reports whether the search of text has its answer, and
// the list would show it: the field is not closed (Esc) nor holding a pick.
func (st *addState) searchAnswered(text string) bool {
	t := strings.TrimSpace(text)
	return !st.shut && (st.skip == "" || t != st.skip) && len([]rune(t)) >= searchMin &&
		st.due.IsZero() && st.answered == t && st.asked == t
}

// fireSearch sends the search once the typing has paused; until then it asks
// for a frame at that time.
func (st *addState) fireSearch(c *ui.Context, act Actions) {
	if st.due.IsZero() {
		return
	}
	now := c.Now()
	if now.Before(st.due) {
		c.After(st.due.Sub(now))
		return
	}
	st.due = time.Time{}
	if st.dueText != st.asked {
		st.asked, st.answered, st.failed = st.dueText, "", false
		act.SearchModels(st.asked)
	}
}

// pick is the text a row puts in the field. A GGUF repository gets its
// ":" to list the variants after; a repository without variants, and a
// variant, are complete.
func (st *addState) pick(text string, r addRow) string {
	switch {
	case r.isVar:
		repo, _, _ := splitName(text)
		return repo + ":" + r.variant.Tag
	case r.repo.GGUF():
		return r.repo.ID + ":"
	}
	return r.repo.ID
}

// accept puts a row in the field through set, which the caller makes: the
// field's own setter while a key is handled, else the bound text.
func (p *Panel) addAccept(r addRow, set func(string)) {
	st := &p.add
	text := st.pick(p.addText, r)
	if !r.repo.GGUF() || r.isVar {
		st.skip = text
	}
	st.sel, st.focus = -1, true
	set(text)
	p.addBad = false
	// A checkpoint without variants is looked at too: its quantization
	// and size.
	if !r.isVar && !r.repo.GGUF() && !strings.EqualFold(r.repo.ID, st.varsAsked) {
		st.varsAsked, st.vars, st.varsFor, st.info, st.varsErr = r.repo.ID, nil, "", hf.RepoInfo{}, false
		p.act.ModelVariants(r.repo.ID)
	}
	p.gen++
}

// addField is the open "Add model…" field: the input, the list under it and
// the note. example is the placeholder.
func (p *Panel) addField(c *ui.Context, f *frame, inPopup bool, example string) {
	t, m := f.pal, f.m
	st := &p.add
	col := ui.Column(c).Key("add-model-field").Gap(addFieldGap)
	if inPopup {
		col.Padding(addPopPadTop, 8, addPopPadBottom, 8)
	}
	col.Children(func() {
		border := t.popBorder
		if p.addBad {
			border = t.err
		}
		box := ui.Box(c).Key("add-model-box")
		var in ui.Element
		box.Children(func() {
			in = ui.TextInputBase(c.Key("add-model-input"), &p.addText).Height(28).Radius(8).Border(1, border).
				Background(t.field).TextColor(t.text).FontSize(12).Font(mono).PaddingX(8).Direction(ui.LeftToRight).
				Placeholder(example).Label(m.AddModel()).AutoFocus()
			if p.addText != "" {
				// The clear button over the end of the field.
				in.PaddingEnd(28)
				clr := ui.ButtonBase(c).Key("add-model-clear").Absolute().End(4).Top(4).Size(20, 20).Radius(10).
					KeepFocus().Cursor(ui.CursorPointer).TextColor(t.muted).Label(m.ClearField()).Tooltip(m.ClearField())
				if clr.Hovered() {
					clr.TextColor(t.soft)
				}
				clr.OnClick(func() {
					p.addText, p.addBad = "", false
					st.focus = true
					p.gen++
				}).Children(func() { ico(c, "circle-x", 2, 16) })
			}
		})
		if p.addBad {
			in.Error(m.AddModelBad())
		}
		if in.Changed() {
			p.addBad = false
		}
		if !p.measured {
			if p.addText != st.seen {
				st.typed(c, p.act, p.addText)
			}
			st.fireSearch(c, p.act)
		}
		if st.focus {
			st.focus = false
			n := len([]rune(p.addText))
			in.Focus().SetTextSelection(n, n)
		}

		rows := st.rowsFor(p.addText)
		st.rows, st.sel = len(rows), min(st.sel, len(rows)-1)
		_, _, colon := splitName(p.addText)
		repo, _, _ := splitName(p.addText)
		st.loading = colon && repo != "" && strings.EqualFold(repo, st.varsAsked) && !strings.EqualFold(repo, st.varsFor)

		in.Role(ui.RoleComboBox).Expanded(len(rows) > 0)
		in.OnTextKey(func(k ui.TextKey) bool {
			return p.addKey(k, rows)
		})
		// Esc closes the list first, then the field.
		if in.OverlayShortcut(0, ui.KeyEscape) || in.Shortcut(0, ui.KeyEscape) {
			if st.showing {
				st.shut, st.showing = true, false
				p.gen++
			} else {
				p.addingModel, p.addText, p.addBad = false, "", false
				st.reset(p.act)
			}
		}
		if in.Submitted() {
			p.submitModel()
		}

		st.showing = len(rows) > 0
		// The list, or what shows in its place: the summary of the chosen
		// model, a note, or the hint at what to type.
		ui.Column(c).Key("add-model-area").Gap(addFieldGap).Children(func() {
			if len(rows) > 0 {
				p.addList(c, f, rows)
			}
			ch, chosen := p.addChosen(rows)
			st.summary, st.hint = chosen, false
			if chosen {
				p.addSummary(c, f, ch)
				return
			}
			note, tone := "", t.muted
			switch {
			case p.addBad:
				note, tone = m.AddModelBad(), t.err
			case st.loading || (!colon && repo != "" && strings.EqualFold(repo, st.varsAsked) && st.varsFor == ""):
				note = m.AddModelLoading()
			case len(rows) > 0:
			case colon && repo != "" && strings.EqualFold(repo, st.varsFor) && st.varsErr:
				note = m.AddModelUnreachable()
			case colon && repo != "" && strings.EqualFold(repo, st.varsFor) && st.full && len(st.vars) == 0:
				note = m.AddModelNoVariants(p.view.Engine)
			case !colon && st.searchAnswered(p.addText) && st.failed:
				note = m.AddModelUnreachable()
			case !colon && st.searchAnswered(p.addText):
				note = m.AddModelNothing()
			}
			if note != "" {
				txt(c, note, 11).TextColor(tone).LineHeight(1.35).Role(ui.RoleStatus)
			}
			if len(rows) == 0 && len([]rune(strings.TrimSpace(p.addText))) < searchMin {
				st.hint = true
				addEmpty(c, f)
			}
		})
	})
}

// addChoice is the model the field holds once its name is complete.
type addChoice struct {
	md    engine.Model
	tag   string
	bytes int64       // the weights; 0 = not known yet
	info  hf.RepoInfo // what the hub said of the repository
	known bool        // the hub's answer is in
}

// addChosen is the model the field holds, when the engine accepts its name
// and no list is open. owner/repo:variant is complete with its variant; a
// repository without variants once the hub has said what it is.
func (p *Panel) addChosen(rows []addRow) (ch addChoice, ok bool) {
	st := &p.add
	text := strings.TrimSpace(p.addText)
	if len(rows) > 0 || text == "" || p.addBad {
		return ch, false
	}
	md, err := p.act.ParseModel(text)
	if err != nil {
		return ch, false
	}
	repo, tag, colon := splitName(text)
	ch = addChoice{md: md, tag: strings.TrimSpace(tag), known: repo != "" && strings.EqualFold(repo, st.varsFor)}
	if ch.known {
		ch.info = st.info
	}
	switch {
	case colon && ch.tag == "":
		return ch, false
	case colon:
		for _, v := range st.vars {
			if ch.known && strings.EqualFold(v.Tag, ch.tag) {
				ch.bytes = v.Bytes
			}
		}
	default:
		if !ch.known || ch.info.GGUF {
			return ch, false
		}
		ch.bytes = ch.info.Weights
	}
	return ch, true
}

// downloadBlocked reports whether a download cannot start now
// (downloadBlock).
func (p *Panel) downloadBlocked() bool { return p.downloadBlock(nil) != "" }

// downloadBlock is why a download cannot start now, "" when it can. A
// download apart from the server waits only for an update, a switch or an
// install (behind another download it queues); one on the server's start
// also for a server that is stopping or not ours. m nil gives a non-empty
// reason without words.
func (p *Panel) downloadBlock(m *locales.Messages) string {
	v := p.view
	say := func(f func(locales.Messages) string) string {
		if m == nil {
			return "blocked"
		}
		return f(*m)
	}
	switch {
	case v.Switching || v.Update == core.UpdateUpdating || isInstallPhase(v.Phase):
		if !v.DownloadApart {
			return say(locales.Messages.AddModelBlocked)
		}
		return say(func(m locales.Messages) string { return m.AddModelWait(v.Engine) })
	case !v.DownloadApart && (v.Phase == core.PhaseStopping || v.Phase == core.PhaseExternal):
		return say(locales.Messages.AddModelBlocked)
	}
	return ""
}

// textOnlyRepo reports whether a model of the repository the hub described
// has no image input to download: a GGUF repository without a vision
// projector.
func textOnlyRepo(info hf.RepoInfo) bool { return info.GGUF && info.Vision == 0 }

// addSummary is the chosen model under the field: its name, variant and
// size, what it needs in memory (a warning when it does not fit) and the
// button that downloads it, as Enter does.
func (p *Panel) addSummary(c *ui.Context, f *frame, ch addChoice) {
	t, m := f.pal, f.m
	name := ch.md.Name
	if name == "" {
		name = ch.md.ID
	}
	var sub []string
	if ch.md.Owner != "" {
		sub = append(sub, ch.md.Owner)
	}
	if ch.md.Variant != "" {
		sub = append(sub, ch.md.Variant)
	}
	if ch.bytes > 0 {
		sub = append(sub, format.GB(f.loc, ch.bytes))
	}
	// No vision projector: downloaded and served for text only.
	textOnly := ch.known && p.add.full && textOnlyRepo(ch.info)
	unsupported := ch.known && ch.info.Unsupported && ch.tag == ""
	loading := !ch.known && ch.tag != ""
	reason := ""
	switch {
	case p.downloadBlocked():
		reason = p.downloadBlock(&m)
	case unsupported:
		reason = m.AddModelQuantBad(p.view.Engine, ch.info.Quant)
	}
	ui.Row(c).Key("add-model-summary").AlignItems(ui.Center).Gap(10).Padding(8, 8, 8, 10).Radius(8).
		Border(1, t.popBorder).Background(t.field).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
			txt(c, name, 12.5).FontWeight(600).SingleLine().Direction(ui.LeftToRight).AlignSelf(ui.Start)
			if len(sub) > 0 {
				txt(c, strings.Join(sub, " · "), 11).TextColor(t.muted).SingleLine().AlignSelf(ui.Start)
			}
			if textOnly {
				txt(c, m.TextOnly(), 11).TextColor(t.soft).SingleLine().AlignSelf(ui.Start)
			}
			switch {
			case unsupported:
				txt(c, reason, 11).TextColor(t.err).LineHeight(1.35).Role(ui.RoleStatus)
			case loading:
				txt(c, m.AddModelLoading(), 11).TextColor(t.muted).LineHeight(1.35).Role(ui.RoleStatus)
			case ch.bytes > 0:
				p.needNote(c, f, repoModel(ch.info, ch.bytes), engine.Config{})
			}
		})
		b := btn(c).Key("add-model-download").Height(26).Radius(7).PaddingStart(8).PaddingEnd(10).Gap(5).Shrink(0).
			Background(t.startBg).TextColor(t.startText).KeepFocus().Label(m.Download() + " " + name)
		if reason != "" {
			b.Disabled(true).Cursor(ui.CursorDefault).Tooltip(reason).Description(reason)
		} else {
			b.OnClick(p.submitModel)
		}
		b.Children(func() {
			ico(c, "download", 2.2, 13)
			txt(c, m.Download(), 12).FontWeight(600).NoWrap()
		})
	})
}

// addEmpty is the place of the list while nothing is listed: a hint at
// what to type.
func addEmpty(c *ui.Context, f *frame) {
	t, m := f.pal, f.m
	ui.Row(c).Key("add-model-empty").AlignItems(ui.Start).Gap(6).TextColor(t.muted).Children(func() {
		ico(c, "search", 2, 13).Shrink(0).Margin(1, 0, 0, 0)
		txt(c, m.AddModelEmpty(), 11).LineHeight(1.35).Grow(1).Shrink(1)
	})
}

// addKey moves in the list and takes a row, for the keys it uses; the
// others go on to the field.
func (p *Panel) addKey(k ui.TextKey, rows []addRow) bool {
	st := &p.add
	if k.Mods != 0 || len(rows) == 0 {
		return false
	}
	switch k.Key {
	case ui.KeyDown:
		st.sel = min(st.sel+1, len(rows)-1)
	case ui.KeyUp:
		st.sel = max(st.sel-1, -1)
	case ui.KeyEnter:
		// Enter on no row downloads what is typed.
		if st.sel < 0 || st.sel >= len(rows) {
			return false
		}
		p.addAccept(rows[st.sel], func(s string) { p.addText = s; k.SetText(s) })
	case ui.KeyTab:
		p.addAccept(rows[max(st.sel, 0)], func(s string) { p.addText = s; k.SetText(s) })
	default:
		return false
	}
	p.gen++
	return true
}

// addList draws the lines under the field.
func (p *Panel) addList(c *ui.Context, f *frame, rows []addRow) {
	t := f.pal
	st := &p.add
	list := ui.Column(c).Key("add-model-list").KeepFocus().Role(ui.RoleList).Radius(8).Border(1, t.popBorder).
		Background(t.field).Padding(3)
	list.Children(func() {
		// The list takes the room its rows need; it scrolls only where the
		// window, held to the screen, has none left for it.
		body := ui.Column(c)
		if p.maxH > 0 {
			body = ui.Scroll(c).MaxHeight(float32(max(150, p.maxH-addListAbove)))
		}
		body.Children(func() {
			mixed := false
			for _, r := range rows {
				if r.repo.Filter != rows[0].repo.Filter {
					mixed = true
				}
			}
			for i, r := range rows {
				scroll := i == st.sel && st.scrolled != i
				if scroll {
					st.scrolled = i
				}
				click := func() {
					p.addAccept(r, func(s string) { p.addText = s })
					c.AnimationFrame()
				}
				if r.isVar {
					p.variantRow(c, f, r.variant, i == st.sel, scroll, click)
				} else {
					p.repoRow(c, f, r.repo, i == st.sel, mixed, scroll, click)
				}
			}
		})
	})
	if st.sel < 0 {
		st.scrolled = -1
	}
}

// repoRow is a repository: its name, how often it was downloaded and, when
// the list mixes formats, its format.
func (p *Panel) repoRow(c *ui.Context, f *frame, r hf.Repo, sel, mixed, scroll bool, click func()) {
	t, m := f.pal, f.m
	sub := m.ModelDownloads(int(min(r.Downloads, math.MaxInt32)))
	if mixed {
		sub = strings.ToUpper(r.Filter) + " · " + sub
	}
	row := ui.ButtonBase(c).Key("hit-" + r.ID).Height(repoRowH).PaddingX(8).Radius(6).Gap(8).Justify(ui.Start).
		Cursor(ui.CursorPointer).Role(ui.RoleListItem).Label(r.ID + ", " + sub).Selected(sel)
	if sel || row.Hovered() || row.FocusVisible() {
		row.Background(t.rowHover)
	}
	if scroll {
		row.ScrollIntoView()
	}
	row.OnClick(click).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			monoTxt(c, r.ID, 11.5).SingleLine().Direction(ui.LeftToRight).AlignSelf(ui.Start).TextColor(t.text)
			txt(c, sub, 11).TextColor(t.muted).SingleLine().AlignSelf(ui.Start)
		})
	})
}

// variantRow is a variant: its tag and size, and under them what it needs
// in memory with its vision projector, draft and KV cache (engine.Judge),
// as a warning when that does not fit.
func (p *Panel) variantRow(c *ui.Context, f *frame, v hf.Variant, sel, scroll bool, click func()) {
	t := f.pal
	md := repoModel(p.add.info, v.Bytes)
	_, fit := p.judge(md, engine.Config{})
	fits := fit != engine.FitsNot
	size := format.GB(f.loc, v.Bytes)
	row := ui.ButtonBase(c).Key("var-" + v.Tag).MinHeight(30).PaddingX(8).PaddingY(3).Radius(6).Gap(8).Justify(ui.Start).
		Cursor(ui.CursorPointer).Role(ui.RoleListItem).Label(v.Tag + ", " + size).Selected(sel)
	if sel || row.Hovered() || row.FocusVisible() {
		row.Background(t.rowHover)
	}
	if scroll {
		row.ScrollIntoView()
	}
	row.OnClick(click).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				// One that does not fit reads as the suggestions' do: quieter,
				// with the warning.
				ink := t.text
				if !fits {
					ink = t.soft
				}
				monoTxt(c, v.Tag, 12).SingleLine().Direction(ui.LeftToRight).Grow(1).MinWidth(0).TextColor(ink)
				if !fits {
					ico(c, "alert-triangle", 2, 11).Shrink(0).TextColor(t.warnText)
				}
				txt(c, size, 11.5).TextColor(t.muted).NoWrap().Shrink(0)
			})
			p.needNote(c, f, md, engine.Config{})
		})
	})
}

// fits reports whether weights of that many bytes fit in memory: they do
// when they take at most three quarters of it, what macOS lets the GPU use
// by default on most Macs.
func (p *Panel) fits(bytes int64) bool { return p.memory <= 0 || bytes <= p.memory/4*3 }

// ramText is the Mac's memory as Apple sells it: 48 GB is 48 GiB.
func (p *Panel) ramText(f *frame) string {
	return format.GB(f.loc, int64(math.Round(float64(p.memory)/(1<<30)))*1e9)
}
