package render

// LayoutMode controls how outgoing messages appear in the message list.
type LayoutMode string

const (
	LayoutTranscript LayoutMode = "transcript"
	LayoutIM         LayoutMode = "im"
)

func ParseLayoutMode(value string) LayoutMode {
	if value == string(LayoutIM) {
		return LayoutIM
	}
	return LayoutTranscript
}

type MessageRowOpts struct {
	Layout              LayoutMode
	IncludeMediaPreview bool
	BroadcastChannel    bool
	GroupReadMarks      bool
}

func DefaultMessageRowOpts() MessageRowOpts {
	return MessageRowOpts{
		Layout:              LayoutTranscript,
		IncludeMediaPreview: true,
	}
}
