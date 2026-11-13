package web

// The texts a page shows for the outcomes of internal/approvals.
const (
	approvalsMsgNotAllowed = ""
	approvalsMsgDecided    = ""
	approvalsMsgLocked     = ""
	approvalsMsgMaster     = ""
	approvalsMsgLastOwner  = ""
	approvalsMsgSelf       = ""
	approvalsMsgNotFound   = ""
)

func approvalsAnswer(err error) (status int, msg string, ok bool) {
	return 0, "", false
}
