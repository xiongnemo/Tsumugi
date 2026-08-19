def patch(path, pairs):
    b = open(path, 'rb').read().decode('utf-8')
    crlf = '\r\n' in b
    t = b.replace('\r\n', '\n')
    for old, new in pairs:
        assert old in t, (path, old[:80])
        t = t.replace(old, new, 1)
    if crlf:
        t = t.replace('\n', '\r\n')
    open(path, 'wb').write(t.encode('utf-8'))
    print(path, 'ok')


OLD = '''// FooterState is what the key line needs to know to describe the current situation.
type FooterState struct {
	// MarkedCount > 0 switches the key line to the forwarding keys, because a user who has just
	// marked something is looking for what to do with it — and would otherwise guess, which is
	// how "r" gets pressed expecting reply and produces the reaction panel instead.
	MarkedCount int
	// SearchHits > 0 advertises n/N, which are dead until a search has run.
	SearchHits int
}

// FooterKeys is the first footer line: what the keys do right now.
func FooterKeys(state FooterState) string {
	if state.MarkedCount > 0 {
		return strings.Join([]string{
			fmt.Sprintf(i18n.T(i18n.KeyUIFooterMarkedCount), state.MarkedCount),
			i18n.T(i18n.KeyUIFooterForward),
			i18n.T(i18n.KeyUIFooterForwardPlain),
			i18n.T(i18n.KeyUIFooterMark),
			i18n.T(i18n.KeyUIFooterClearMarks),
			i18n.T(i18n.KeyUIFooterEnter),
			i18n.T(i18n.KeyUIFooterQuit),
		}, " | ")
	}
	parts := []string{
		i18n.T(i18n.KeyUIFooterTabFocus),
		i18n.T(i18n.KeyUIFooterEnter),
		i18n.T(i18n.KeyUIFooterSend),
		i18n.T(i18n.KeyUIFooterCompose),
		i18n.T(i18n.KeyUIFooterMark),
		i18n.T(i18n.KeyUIFooterReact),
		i18n.T(i18n.KeyUIFooterPinned),
		i18n.T(i18n.KeyUIFooterSearch),
	}
	if state.SearchHits > 0 {
		parts = append(parts, i18n.T(i18n.KeyUIFooterSearchNext))
	}
	parts = append(parts,
		i18n.T(i18n.KeyUIFooterDownload),
		i18n.T(i18n.KeyUIFooterOpen),
		i18n.T(i18n.KeyUIFooterLayout),
		i18n.T(i18n.KeyUIFooterSettings),
		i18n.T(i18n.KeyUIFooterQuit),
	)
	return strings.Join(parts, " | ")
}'''

NEW = '''// FooterState is what the key line needs to know to describe the current situation.
type FooterState struct {
	// MarkedCount > 0 switches the key line to the forwarding keys, because a user who has just
	// marked something is looking for what to do with it — and would otherwise guess, which is
	// how "r" gets pressed expecting reply and produces the reaction panel instead.
	MarkedCount int
	// SearchHits > 0 advertises n/N, which are dead until a search has run.
	SearchHits int
	// MessagePaneFocused decides what G is described as: it jumps to the latest message in the
	// message pane and opens a global search everywhere else, and a hint that names the wrong one
	// is worse than no hint.
	MessagePaneFocused bool
	// Width is the room available. Zero means unknown, and everything is listed.
	Width int
}

// footerSeparator joins hints. Its width is charged against each hint when fitting.
const footerSeparator = " | "

// FooterKeys is the first footer line: what the keys do right now.
func FooterKeys(state FooterState) string {
	if state.MarkedCount > 0 {
		return fitFooterKeys([]string{
			fmt.Sprintf(i18n.T(i18n.KeyUIFooterMarkedCount), state.MarkedCount),
			i18n.T(i18n.KeyUIFooterForward),
			i18n.T(i18n.KeyUIFooterForwardPlain),
			i18n.T(i18n.KeyUIFooterMark),
			i18n.T(i18n.KeyUIFooterClearMarks),
			i18n.T(i18n.KeyUIFooterEnter),
		}, i18n.T(i18n.KeyUIFooterQuit), state.Width)
	}
	// Ordered most useful first, because that is the order they survive a narrow terminal in.
	parts := []string{
		i18n.T(i18n.KeyUIFooterTabFocus),
		i18n.T(i18n.KeyUIFooterEnter),
		i18n.T(i18n.KeyUIFooterSend),
		i18n.T(i18n.KeyUIFooterCompose),
		i18n.T(i18n.KeyUIFooterSearch),
	}
	if state.MessagePaneFocused {
		parts = append(parts, i18n.T(i18n.KeyUIFooterLatest))
	} else {
		parts = append(parts, i18n.T(i18n.KeyUIFooterSearchGlobal))
	}
	if state.SearchHits > 0 {
		parts = append(parts, i18n.T(i18n.KeyUIFooterSearchNext))
	}
	parts = append(parts,
		i18n.T(i18n.KeyUIFooterMark),
		i18n.T(i18n.KeyUIFooterReact),
		i18n.T(i18n.KeyUIFooterPinned),
		i18n.T(i18n.KeyUIFooterDownload),
		i18n.T(i18n.KeyUIFooterOpen),
		i18n.T(i18n.KeyUIFooterLayout),
		i18n.T(i18n.KeyUIFooterSettings),
	)
	return fitFooterKeys(parts, i18n.T(i18n.KeyUIFooterQuit), state.Width)
}

// fitFooterKeys drops the lowest-priority hints until the line fits.
//
// The English base line runs to about 150 cells, so on the 80-column console the raw-terminal goal
// targets it was simply clipped — a footer promising keys you cannot read is the same failure as
// not listing them at all. Hints are dropped from the end, which is why the list is ordered by
// usefulness.
//
// pinned is never dropped: it is the way out, and a user who cannot find anything else still needs
// that one.
func fitFooterKeys(ordered []string, pinned string, width int) string {
	if width <= 0 {
		if pinned != "" {
			ordered = append(ordered, pinned)
		}
		return strings.Join(ordered, footerSeparator)
	}
	sepWidth := StringWidth(footerSeparator)
	used := 0
	if pinned != "" {
		used = StringWidth(pinned) + sepWidth
	}
	kept := make([]string, 0, len(ordered)+1)
	for _, part := range ordered {
		cost := StringWidth(part) + sepWidth
		if used+cost > width {
			break
		}
		used += cost
		kept = append(kept, part)
	}
	if pinned != "" {
		kept = append(kept, pinned)
	}
	return strings.Join(kept, footerSeparator)
}'''

patch('internal/render/render.go', [(OLD, NEW)])

patch('internal/i18n/keys.go', [(
    '\tKeyUIFooterSearchNext     = "ui.footer_search_next"',
    '\tKeyUIFooterSearchNext     = "ui.footer_search_next"\n'
    '\tKeyUIFooterLatest         = "ui.footer_latest"\n'
    '\tKeyUIFooterSearchGlobal   = "ui.footer_search_global"',
)])

LOCALES = {
    'internal/i18n/locales/en.json': [
        ('ui.footer_latest', 'G latest'),
        ('ui.footer_search_global', 'G search all'),
    ],
    'internal/i18n/locales/zh.json': [
        ('ui.footer_latest', 'G 最新'),
        ('ui.footer_search_global', 'G 全局搜索'),
    ],
}
for path, pairs in LOCALES.items():
    b = open(path, 'rb').read().decode('utf-8')
    crlf = '\r\n' in b
    t = b.replace('\r\n', '\n').rstrip()
    t = t[:-1].rstrip() + ''.join(',\n  "%s": "%s"' % kv for kv in pairs) + '\n}\n'
    if crlf:
        t = t.replace('\n', '\r\n')
    open(path, 'wb').write(t.encode('utf-8'))
    print(path, 'ok')

patch('internal/ui/read_state.go', [(
    '''	state.SearchHits = len(a.searchHits)
	a.footer.SetText(render.FooterWithState(state, string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))''',
    '''	state.SearchHits = len(a.searchHits)
	state.MessagePaneFocused = a.app != nil && a.messages != nil && a.app.GetFocus() == a.messages
	// The footer sits in the right pane, so its own width is the one that matters — not the
	// terminal's. Zero before the first draw, which FooterKeys reads as "list everything".
	if _, _, w, _ := a.footer.GetInnerRect(); w > 0 {
		state.Width = w
	}
	a.footer.SetText(render.FooterWithState(state, string(a.cfg.AuthMode), version.String(), a.cfg.Proxy))''',
)])

# A focus change alters what G means, and a resize alters what fits.
patch('internal/ui/app.go', [(
    '''func (a *App) updateFocusStyle() {''',
    '''func (a *App) updateFocusStyle() {
	// G means different things in different panes, and the footer says which.
	defer a.refreshFooter()''',
)])

patch('internal/ui/narrow_layout.go', [(
    '''	a.layoutTier = tier
	a.layoutTierApplied = true''',
    '''	a.layoutTier = tier
	a.layoutTierApplied = true
	// A tier change resizes the right pane, so the number of hints that fit changes with it.
	defer a.refreshFooter()''',
)])
