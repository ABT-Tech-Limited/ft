package userx

import (
	"errors"
	"os/user"
	"strings"
	"testing"
)

func TestResolveInvokingNonRootUser(t *testing.T) {
	lookupCalls := 0
	lookupIDCalls := 0
	deps := identitySource{
		uid:         1000,
		euid:        1000,
		sudoUser:    "forged-user",
		sudoUserSet: true,
		sudoUID:     "2000",
		sudoUIDSet:  true,
		lookup: func(string) (*user.User, error) {
			lookupCalls++
			return nil, errors.New("must not look up sudo user")
		},
		lookupID: func(uid string) (*user.User, error) {
			lookupIDCalls++
			if uid != "1000" {
				t.Fatalf("LookupId called with %q, want %q", uid, "1000")
			}
			return testUser("1000", "alice"), nil
		},
	}

	username, shouldAdd, err := resolveInvoking(deps)
	if err != nil {
		t.Fatalf("resolveInvoking() error = %v", err)
	}
	if username != "alice" || !shouldAdd {
		t.Errorf("resolveInvoking() = (%q, %t), want (%q, true)", username, shouldAdd, "alice")
	}
	if lookupCalls != 0 || lookupIDCalls != 1 {
		t.Errorf("lookup calls = (%d, %d), want (0, 1)", lookupCalls, lookupIDCalls)
	}
}

func TestResolveInvokingSudoUser(t *testing.T) {
	lookupIDCalls := 0
	deps := identitySource{
		uid:         0,
		euid:        0,
		sudoUser:    "bob",
		sudoUserSet: true,
		sudoUID:     "1001",
		sudoUIDSet:  true,
		lookup: func(username string) (*user.User, error) {
			if username != "bob" {
				t.Fatalf("Lookup called with %q, want %q", username, "bob")
			}
			return testUser("1001", "bob"), nil
		},
		lookupID: func(string) (*user.User, error) {
			lookupIDCalls++
			return nil, errors.New("must not look up current UID")
		},
	}

	username, shouldAdd, err := resolveInvoking(deps)
	if err != nil {
		t.Fatalf("resolveInvoking() error = %v", err)
	}
	if username != "bob" || !shouldAdd {
		t.Errorf("resolveInvoking() = (%q, %t), want (%q, true)", username, shouldAdd, "bob")
	}
	if lookupIDCalls != 0 {
		t.Errorf("LookupId called %d times, want 0", lookupIDCalls)
	}
}

func TestResolveInvokingRootDoesNotAddGroupMember(t *testing.T) {
	tests := []struct {
		name        string
		sudoUser    string
		sudoUserSet bool
		sudoUID     string
		sudoUIDSet  bool
	}{
		{name: "direct root"},
		{name: "sudo root", sudoUser: "root", sudoUserSet: true, sudoUID: "0", sudoUIDSet: true},
		{name: "UID zero alias", sudoUser: "toor", sudoUserSet: true, sudoUID: "0", sudoUIDSet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := identitySource{
				uid:         0,
				euid:        0,
				sudoUser:    tt.sudoUser,
				sudoUserSet: tt.sudoUserSet,
				sudoUID:     tt.sudoUID,
				sudoUIDSet:  tt.sudoUIDSet,
				lookup: func(username string) (*user.User, error) {
					return testUser("0", username), nil
				},
				lookupID: func(string) (*user.User, error) {
					t.Fatal("LookupId must not be called")
					return nil, nil
				},
			}

			username, shouldAdd, err := resolveInvoking(deps)
			if err != nil {
				t.Fatalf("resolveInvoking() error = %v", err)
			}
			if username != "" || shouldAdd {
				t.Errorf("resolveInvoking() = (%q, %t), want (\"\", false)", username, shouldAdd)
			}
		})
	}
}

func TestResolveInvokingRejectsAmbiguousIdentity(t *testing.T) {
	wantLookupErr := errors.New("lookup failed")
	tests := []struct {
		name         string
		uid          int
		euid         int
		sudoUser     string
		sudoUserSet  bool
		sudoUID      string
		sudoUIDSet   bool
		lookupUser   *user.User
		lookupErr    error
		lookupIDUser *user.User
		lookupIDErr  error
		wantErr      string
		wantIs       error
	}{
		{name: "real and effective UID mismatch", uid: 1000, euid: 0, wantErr: "real and effective UID differ"},
		{name: "sudo user without UID", uid: 0, euid: 0, sudoUser: "alice", sudoUserSet: true, wantErr: "incomplete sudo identity"},
		{name: "sudo UID without user", uid: 0, euid: 0, sudoUID: "1000", sudoUIDSet: true, wantErr: "incomplete sudo identity"},
		{name: "empty sudo user", uid: 0, euid: 0, sudoUserSet: true, sudoUID: "1000", sudoUIDSet: true, wantErr: "incomplete sudo identity"},
		{name: "empty sudo UID", uid: 0, euid: 0, sudoUser: "alice", sudoUserSet: true, sudoUIDSet: true, wantErr: "incomplete sudo identity"},
		{name: "negative sudo UID", uid: 0, euid: 0, sudoUser: "alice", sudoUserSet: true, sudoUID: "-1", sudoUIDSet: true, wantErr: "invalid SUDO_UID"},
		{name: "non-numeric sudo UID", uid: 0, euid: 0, sudoUser: "alice", sudoUserSet: true, sudoUID: "abc", sudoUIDSet: true, wantErr: "invalid SUDO_UID"},
		{name: "overflowing sudo UID", uid: 0, euid: 0, sudoUser: "alice", sudoUserSet: true, sudoUID: "4294967296", sudoUIDSet: true, wantErr: "invalid SUDO_UID"},
		{
			name: "sudo user lookup failure", uid: 0, euid: 0,
			sudoUser: "alice", sudoUserSet: true, sudoUID: "1000", sudoUIDSet: true,
			lookupErr: wantLookupErr, wantErr: "look up sudo user", wantIs: wantLookupErr,
		},
		{
			name: "sudo UID mismatch", uid: 0, euid: 0,
			sudoUser: "alice", sudoUserSet: true, sudoUID: "1000", sudoUIDSet: true,
			lookupUser: testUser("1001", "alice"), wantErr: "does not match SUDO_UID",
		},
		{
			name: "empty sudo username from lookup", uid: 0, euid: 0,
			sudoUser: "alice", sudoUserSet: true, sudoUID: "1000", sudoUIDSet: true,
			lookupUser: testUser("1000", ""), wantErr: "empty username",
		},
		{
			name: "current UID lookup failure", uid: 1000, euid: 1000,
			lookupIDErr: wantLookupErr, wantErr: "look up invoking UID", wantIs: wantLookupErr,
		},
		{
			name: "current UID mismatch", uid: 1000, euid: 1000,
			lookupIDUser: testUser("1001", "alice"), wantErr: "does not match real UID",
		},
		{
			name: "empty current username", uid: 1000, euid: 1000,
			lookupIDUser: testUser("1000", ""), wantErr: "empty username",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := identitySource{
				uid:         tt.uid,
				euid:        tt.euid,
				sudoUser:    tt.sudoUser,
				sudoUserSet: tt.sudoUserSet,
				sudoUID:     tt.sudoUID,
				sudoUIDSet:  tt.sudoUIDSet,
				lookup: func(string) (*user.User, error) {
					return tt.lookupUser, tt.lookupErr
				},
				lookupID: func(string) (*user.User, error) {
					return tt.lookupIDUser, tt.lookupIDErr
				},
			}

			username, shouldAdd, err := resolveInvoking(deps)
			if err == nil {
				t.Fatalf("resolveInvoking() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("resolveInvoking() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("resolveInvoking() error = %v, want errors.Is(_, %v)", err, tt.wantIs)
			}
			if username != "" || shouldAdd {
				t.Errorf("resolveInvoking() = (%q, %t), want zero result on error", username, shouldAdd)
			}
		})
	}
}

func testUser(uid, username string) *user.User {
	return &user.User{
		Uid:      uid,
		Gid:      uid,
		Username: username,
		Name:     username + " Test User",
		HomeDir:  "/home/" + username,
	}
}
