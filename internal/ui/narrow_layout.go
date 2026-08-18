package ui

const (
	// Nominal widths of the two left rails.
	folderRailWidth = 16
	chatRailWidth   = 34
	// messagePaneMinWidth is the narrowest the message pane may become before chrome starts
	// being dropped. Below this, messages wrap to a few words per line and reading is the thing
	// that suffers — which is the opposite of the tradeoff we want.
	messagePaneMinWidth = 40
	// chatRailNarrowWidth is what the chat list shrinks to once the folder rail is gone.
	chatRailNarrowWidth = 24
)

// layoutMode describes how much left-hand chrome fits.
type layoutTier int

const (
	// layoutFull shows the folder rail and a full-width chat list.
	layoutFull layoutTier = iota
	// layoutNoFolders drops the folder rail; folders are still reachable with Tab.
	layoutNoFolders
	// layoutNarrowChats additionally shrinks the chat list.
	layoutNarrowChats
)

// layoutTierForWidth decides how much chrome a terminal width can afford.
//
// A bare Linux console is 80 columns. Folders (16) plus the chat list (34) take 50 of those, plus
// three borders, which leaves the message pane under 30 — unusable. Dropping the folder rail first
// is right because folders are a filter over the chat list rather than content, and Tab still
// reaches them.
func layoutTierForWidth(width int) layoutTier {
	switch {
	case width <= 0:
		// No geometry yet; assume the roomy case and re-evaluate on the first draw.
		return layoutFull
	case width-folderRailWidth-chatRailWidth >= messagePaneMinWidth:
		return layoutFull
	case width-chatRailWidth >= messagePaneMinWidth:
		return layoutNoFolders
	default:
		return layoutNarrowChats
	}
}

// applyLayoutTier resizes the root flex for the current terminal width.
//
// Called from the before-draw hook rather than a resize handler, because tview does not expose one
// and the draw pass is where a new width first becomes visible. It is idempotent, so running every
// frame costs a comparison.
func (a *App) applyLayoutTier(width int) {
	tier := layoutTierForWidth(width)
	if a.layoutTier == tier && a.layoutTierApplied {
		return
	}
	a.layoutTier = tier
	a.layoutTierApplied = true
	if a.root == nil || a.folders == nil || a.chats == nil {
		return
	}
	switch tier {
	case layoutFull:
		a.root.ResizeItem(a.folders, folderRailWidth, 0)
		a.root.ResizeItem(a.chats, chatRailWidth, 0)
	case layoutNoFolders:
		// Zero width with zero proportion hides the item without removing it, so Tab focus and
		// every existing folder call site keep working.
		a.root.ResizeItem(a.folders, 0, 0)
		a.root.ResizeItem(a.chats, chatRailWidth, 0)
	default:
		a.root.ResizeItem(a.folders, 0, 0)
		a.root.ResizeItem(a.chats, chatRailNarrowWidth, 0)
	}
}
