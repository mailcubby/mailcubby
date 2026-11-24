package auth

// Role represents a caller's effective role on a project (SPEC 6.5, FR-050).
type Role string

const (
	// RoleNone indicates the caller has no access to the project.
	RoleNone Role = ""
	// RoleViewer grants read-only access to a project.
	RoleViewer Role = "viewer"
	// RoleMember grants day-to-day message, tag, and SMTP read access.
	RoleMember Role = "member"
	// RoleOwner grants full project management access.
	RoleOwner Role = "owner"
	// RoleAdmin represents an instance administrator with implicit owner access
	// on every project plus instance-level administration rights.
	RoleAdmin Role = "admin"
)

// ValidMemberRole reports whether r is one of the assignable project membership
// roles stored in project_members and invites ("owner", "member", "viewer").
func ValidMemberRole(r Role) bool {
	switch r {
	case RoleViewer, RoleMember, RoleOwner:
		return true
	default:
		return false
	}
}

// RoleRank returns an ordering value for project roles so invite acceptance can
// upgrade an existing membership without ever downgrading it (SPEC FR-056).
func RoleRank(r Role) int {
	switch r {
	case RoleViewer:
		return 1
	case RoleMember:
		return 2
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 4
	default:
		return 0
	}
}

// Action identifies a permission-gated operation in SPEC FR-050.
type Action string

const (
	// ActionReadMessages covers viewing a project, listing/reading messages,
	// previewing HTML, downloading attachments, listing tags, and waiting for messages.
	ActionReadMessages Action = "read_messages"
	// ActionReadTechnical covers raw source, headers, HTML source, .eml downloads,
	// and technical fields in message detail (gated by viewer_hide_technical for viewers).
	ActionReadTechnical Action = "read_technical"
	// ActionWriteMessages covers marking seen/unseen, managing tags, deleting
	// messages, and sending test emails.
	ActionWriteMessages Action = "write_messages"
	// ActionReadSMTP covers viewing SMTP connection info and credential usernames.
	ActionReadSMTP Action = "read_smtp"
	// ActionRevealSMTP covers revealing SMTP credential passwords
	// (gated by members_can_reveal_smtp for members).
	ActionRevealSMTP Action = "reveal_smtp"
	// ActionManageSMTP covers creating, rotating, disabling, and deleting SMTP credentials.
	ActionManageSMTP Action = "manage_smtp"
	// ActionListMembers covers viewing project members.
	ActionListMembers Action = "list_members"
	// ActionManageMembers covers inviting, changing roles, removing members,
	// and revoking invites.
	ActionManageMembers Action = "manage_members"
	// ActionManageProject covers editing project settings/retention, renaming,
	// and deleting a project.
	ActionManageProject Action = "manage_project"
	// ActionReadProjectAudit covers reading a project's audit log.
	ActionReadProjectAudit Action = "read_project_audit"
	// ActionLeaveProject covers leaving a project (SPEC FR-051).
	ActionLeaveProject Action = "leave_project"
	// ActionAdmin covers instance-level administration (users, settings, all projects, audit, stats).
	ActionAdmin Action = "admin"
)

// ProjectSettings contains the per-project permission flags evaluated by Allowed (ARCHITECTURE 3.9).
type ProjectSettings struct {
	ViewerHideTechnical  bool
	MembersCanRevealSMTP bool
}

// Allowed reports whether a caller with role may perform action a on a project
// configured with settings p (SPEC FR-050, ARCHITECTURE 3.9).
func Allowed(role Role, a Action, p ProjectSettings) bool {
	switch a {
	case ActionReadMessages, ActionLeaveProject:
		return role == RoleViewer || role == RoleMember || role == RoleOwner || role == RoleAdmin
	case ActionReadTechnical:
		if role == RoleViewer {
			return !p.ViewerHideTechnical
		}
		return role == RoleMember || role == RoleOwner || role == RoleAdmin
	case ActionWriteMessages, ActionReadSMTP, ActionListMembers:
		return role == RoleMember || role == RoleOwner || role == RoleAdmin
	case ActionRevealSMTP:
		if role == RoleMember {
			return p.MembersCanRevealSMTP
		}
		return role == RoleOwner || role == RoleAdmin
	case ActionManageSMTP, ActionManageMembers, ActionManageProject, ActionReadProjectAudit:
		return role == RoleOwner || role == RoleAdmin
	case ActionAdmin:
		return role == RoleAdmin
	default:
		return false
	}
}

// EffectiveTokenRole computes the effective project role for an API token caller
// given the token owner's own project role and the token scope (SPEC FR-066):
//   - "read" acts like viewer (provided the user has project access).
//   - "write" acts like member (never owner), or viewer if the user is a viewer.
func EffectiveTokenRole(userRole Role, scope string) Role {
	if RoleRank(userRole) < RoleRank(RoleViewer) {
		return RoleNone
	}
	switch scope {
	case APITokenScopeRead:
		return RoleViewer
	case APITokenScopeWrite:
		if userRole == RoleViewer {
			return RoleViewer
		}
		return RoleMember
	default:
		return RoleNone
	}
}

// CanReadTechnical reports whether a caller with effectiveRole (derived from a
// user whose own project role is userRole) may view technical message details
// (raw, eml, html source, headers, envelope, parse_warnings).
// Per SPEC FR-066, viewer_hide_technical applies only when the user's own
// project role is viewer (a read token of a member can still read technical views).
func CanReadTechnical(effectiveRole, userRole Role, p ProjectSettings) bool {
	if RoleRank(effectiveRole) < RoleRank(RoleViewer) || RoleRank(userRole) < RoleRank(RoleViewer) {
		return false
	}
	if userRole == RoleViewer {
		return !p.ViewerHideTechnical
	}
	return true
}
