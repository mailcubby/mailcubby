package auth

import "testing"

// FR-050: exhaustive permission matrix verification.
func TestAllowedPermissionMatrix(t *testing.T) {
	t.Parallel()

	defaultSettings := ProjectSettings{
		ViewerHideTechnical:  true,
		MembersCanRevealSMTP: false,
	}
	permissiveSettings := ProjectSettings{
		ViewerHideTechnical:  false,
		MembersCanRevealSMTP: true,
	}

	type wantByRole struct {
		none   bool
		viewer bool
		member bool
		owner  bool
		admin  bool
	}

	cases := []struct {
		name     string
		action   Action
		settings ProjectSettings
		want     wantByRole
	}{
		{
			name:     "read messages default",
			action:   ActionReadMessages,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: true, member: true, owner: true, admin: true},
		},
		{
			name:     "leave project",
			action:   ActionLeaveProject,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: true, member: true, owner: true, admin: true},
		},
		{
			name:     "read technical when viewer_hide_technical=true",
			action:   ActionReadTechnical,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: false, member: true, owner: true, admin: true},
		},
		{
			name:     "read technical when viewer_hide_technical=false",
			action:   ActionReadTechnical,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: true, member: true, owner: true, admin: true},
		},
		{
			name:     "write messages",
			action:   ActionWriteMessages,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: false, member: true, owner: true, admin: true},
		},
		{
			name:     "read smtp",
			action:   ActionReadSMTP,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: false, member: true, owner: true, admin: true},
		},
		{
			name:     "reveal smtp when members_can_reveal_smtp=false",
			action:   ActionRevealSMTP,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: true, admin: true},
		},
		{
			name:     "reveal smtp when members_can_reveal_smtp=true",
			action:   ActionRevealSMTP,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: true, owner: true, admin: true},
		},
		{
			name:     "manage smtp",
			action:   ActionManageSMTP,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: true, admin: true},
		},
		{
			name:     "list members",
			action:   ActionListMembers,
			settings: defaultSettings,
			want:     wantByRole{none: false, viewer: false, member: true, owner: true, admin: true},
		},
		{
			name:     "manage members",
			action:   ActionManageMembers,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: true, admin: true},
		},
		{
			name:     "manage project",
			action:   ActionManageProject,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: true, admin: true},
		},
		{
			name:     "read project audit",
			action:   ActionReadProjectAudit,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: true, admin: true},
		},
		{
			name:     "instance admin",
			action:   ActionAdmin,
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: false, admin: true},
		},
		{
			name:     "unknown action denied",
			action:   Action("unknown"),
			settings: permissiveSettings,
			want:     wantByRole{none: false, viewer: false, member: false, owner: false, admin: false},
		},
	}

	roles := []struct {
		name string
		role Role
		pick func(wantByRole) bool
	}{
		{"none", RoleNone, func(w wantByRole) bool { return w.none }},
		{"viewer", RoleViewer, func(w wantByRole) bool { return w.viewer }},
		{"member", RoleMember, func(w wantByRole) bool { return w.member }},
		{"owner", RoleOwner, func(w wantByRole) bool { return w.owner }},
		{"admin", RoleAdmin, func(w wantByRole) bool { return w.admin }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, rc := range roles {
				got := Allowed(rc.role, tc.action, tc.settings)
				want := rc.pick(tc.want)
				if got != want {
					t.Errorf("Allowed(%q, %q, %+v) = %v, want %v", rc.role, tc.action, tc.settings, got, want)
				}
			}
		})
	}
}

func TestValidMemberRoleAndRank(t *testing.T) {
	t.Parallel()

	if !ValidMemberRole(RoleViewer) || !ValidMemberRole(RoleMember) || !ValidMemberRole(RoleOwner) {
		t.Fatal("expected viewer, member, owner to be valid member roles")
	}
	if ValidMemberRole(RoleNone) || ValidMemberRole(RoleAdmin) || ValidMemberRole(Role("bogus")) {
		t.Fatal("expected none, admin, bogus to be invalid member roles")
	}
	if RoleRank(RoleNone) >= RoleRank(RoleViewer) ||
		RoleRank(RoleViewer) >= RoleRank(RoleMember) ||
		RoleRank(RoleMember) >= RoleRank(RoleOwner) ||
		RoleRank(RoleOwner) >= RoleRank(RoleAdmin) {
		t.Fatal("role ranks are not strictly increasing")
	}
}

// FR-066: effective token role capping and viewer_hide_technical rule.
func TestEffectiveTokenRoleAndCanReadTechnical(t *testing.T) {
	t.Parallel()

	roleCases := []struct {
		name     string
		userRole Role
		scope    string
		want     Role
	}{
		{"none + read", RoleNone, APITokenScopeRead, RoleNone},
		{"none + write", RoleNone, APITokenScopeWrite, RoleNone},
		{"viewer + read", RoleViewer, APITokenScopeRead, RoleViewer},
		{"viewer + write capped to viewer", RoleViewer, APITokenScopeWrite, RoleViewer},
		{"member + read capped to viewer", RoleMember, APITokenScopeRead, RoleViewer},
		{"member + write", RoleMember, APITokenScopeWrite, RoleMember},
		{"owner + read capped to viewer", RoleOwner, APITokenScopeRead, RoleViewer},
		{"owner + write capped to member", RoleOwner, APITokenScopeWrite, RoleMember},
		{"admin + read capped to viewer", RoleAdmin, APITokenScopeRead, RoleViewer},
		{"admin + write capped to member", RoleAdmin, APITokenScopeWrite, RoleMember},
		{"unknown scope", RoleOwner, "owner", RoleNone},
	}

	for _, tc := range roleCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := EffectiveTokenRole(tc.userRole, tc.scope); got != tc.want {
				t.Errorf("EffectiveTokenRole(%q, %q) = %q, want %q", tc.userRole, tc.scope, got, tc.want)
			}
		})
	}

	hideOn := ProjectSettings{ViewerHideTechnical: true}
	hideOff := ProjectSettings{ViewerHideTechnical: false}

	// Member read token has effectiveRole=viewer, userRole=member -> allowed even when viewer_hide_technical=true.
	if !CanReadTechnical(RoleViewer, RoleMember, hideOn) {
		t.Error("expected member read token to read technical views even when viewer_hide_technical=true")
	}
	if !CanReadTechnical(RoleViewer, RoleOwner, hideOn) {
		t.Error("expected owner read token to read technical views even when viewer_hide_technical=true")
	}
	if CanReadTechnical(RoleViewer, RoleViewer, hideOn) {
		t.Error("expected viewer with viewer_hide_technical=true to be denied technical views")
	}
	if !CanReadTechnical(RoleViewer, RoleViewer, hideOff) {
		t.Error("expected viewer with viewer_hide_technical=false to be allowed technical views")
	}
	if CanReadTechnical(RoleNone, RoleMember, hideOff) {
		t.Error("expected RoleNone effective role to be denied")
	}
}
