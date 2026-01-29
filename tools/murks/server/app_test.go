package server

import (
	"testing"
)

func TestRunAppSQLPermissions(t *testing.T) {
	h := NewHarness(t, false)
	owner, member, anon := User{ID: "owner"}, User{ID: "member"}, User{ID: "guest"}
	x := h.CreateApp(owner.ID, App{
		Query: map[string]string{
			"public:1":    "SELECT 'public:1' AS v",
			"member:2":    "SELECT 'member:2' AS v",
			"owner:3":     "SELECT 'owner:3' AS v",
			"none:0":      "SELECT 'none:0' AS v",
			"no-lvl":      "SELECT 'no-lvl' AS v",
			"bad-lvl:abc": "SELECT 'bad-lvl:abc' AS v",
		},
		Users: []string{member.ID},
	})
	for _, tc := range []struct {
		User
		action     string
		public, ok bool
	}{
		{owner, "owner:3", false, true},
		{member, "member:2", false, true},
		{member, "owner:3", false, false},
		{anon, "public:1", false, false},
		{anon, "public:1", true, true},
		{anon, "none:0", true, false},         // default to 3
		{anon, "none:0", false, false},        // default to 3
		{member, "no-lvl", false, false},      // default to 3
		{anon, "no-lvl", false, false},        // default to 3
		{owner, "no-lvl", false, true},        // default to 3
		{member, "bad-lvl:abc", false, false}, // default to 3
	} {
		t.Run("with "+tc.User.ID+" call "+tc.action, func(t *testing.T) {
			if err := h.apps.Update(App{ID: x.ID, IsPublic: tc.public}, "IsPublic"); err != nil {
				t.Fatal(err)
			}
			lvl := h.getLevel(tc.User, x.ID)
			v, err := h.RunAppSQL(h.Context(), tc.User, x.ID, "query", tc.action, nil, lvl)
			if (err == nil) != tc.ok || (err == nil && v.([]map[string]any)[0]["v"] != tc.action) {
				t.Fatalf("user=%s action=%s public=%v: want ok=%v, got v=%v err=%v",
					tc.ID, tc.action, tc.public, tc.ok, v, err)
			}
		})
	}
}
