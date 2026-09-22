package cvs

// StatusKind is what one of `cvs status`'s "Status:" phrases means.
//
// CVS's vocabulary is CVS's, so it is classified once, here, rather
// than in each place that wants to display it. It used to be two
// tables — one in the TUI, one in the compare view — and they had
// already drifted: the TUI's copy had never heard of "Needs Checkout",
// the phrase CVS uses for a tracked file that is missing from the
// working copy. Every file a user deleted by hand therefore lost its
// status the moment its directory was scanned, and vanished from the
// listing.
type StatusKind int

const (
	// StatusUnclassified is a phrase this table does not know. Callers
	// decide what to do with it; none of them should guess.
	StatusUnclassified StatusKind = iota
	StatusUpToDate
	StatusModified
	StatusAdded
	StatusRemoved
	// StatusNeedsUpdate: the server has content this working copy does
	// not — either never checked out, deleted locally, or simply
	// behind. `cvs update` resolves all three.
	StatusNeedsUpdate
	// StatusNeedsMerge: both sides changed, and CVS has not tried to
	// combine them yet.
	StatusNeedsMerge
	// StatusConflict: CVS merged and left markers behind.
	StatusConflict
	// StatusUnknown: CVS does not track this file (`cvs status` says
	// "Unknown" where `cvs update -n` says "?").
	StatusUnknown
)

// statusPhrases maps every "Status:" phrase cvs emits that we act on.
var statusPhrases = map[string]StatusKind{
	"Up-to-date":                  StatusUpToDate,
	"Locally Modified":            StatusModified,
	"Locally Added":               StatusAdded,
	"Locally Removed":             StatusRemoved,
	"Needs Checkout":              StatusNeedsUpdate,
	"Needs Patch":                 StatusNeedsUpdate,
	"Needs Update":                StatusNeedsUpdate,
	"Needs Merge":                 StatusNeedsMerge,
	"File had conflicts on merge": StatusConflict,
	"Unresolved Conflict":         StatusConflict,
	"Unknown":                     StatusUnknown,
}

// ClassifyStatus turns a `cvs status` phrase into what it means.
func ClassifyStatus(phrase string) StatusKind {
	return statusPhrases[phrase]
}

// StatusCode is the one-letter code the file listing shows, and the
// same alphabet `cvs update -n` prints. Empty means "nothing to show":
// an up-to-date file, or a phrase this table does not know.
func (k StatusKind) StatusCode() string {
	switch k {
	case StatusModified, StatusNeedsMerge:
		// A file that needs merging has local changes either way, and
		// the listing's job is to say "this one is not clean".
		return "M"
	case StatusAdded:
		return "A"
	case StatusRemoved:
		return "R"
	case StatusNeedsUpdate:
		return "U"
	case StatusConflict:
		return "C"
	case StatusUnknown:
		return "?"
	default:
		return ""
	}
}
