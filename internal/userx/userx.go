// Package userx resolves the non-root user who invoked ft.
package userx

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

type identitySource struct {
	uid         int
	euid        int
	sudoUser    string
	sudoUserSet bool
	sudoUID     string
	sudoUIDSet  bool
	lookup      func(string) (*user.User, error)
	lookupID    func(string) (*user.User, error)
}

// Invoking returns the verified non-root user who launched ft. Direct root
// invocations return shouldAdd=false because there is no user to infer safely.
func Invoking() (username string, shouldAdd bool, err error) {
	sudoUser, sudoUserSet := os.LookupEnv("SUDO_USER")
	sudoUID, sudoUIDSet := os.LookupEnv("SUDO_UID")
	return resolveInvoking(identitySource{
		uid:         os.Getuid(),
		euid:        os.Geteuid(),
		sudoUser:    sudoUser,
		sudoUserSet: sudoUserSet,
		sudoUID:     sudoUID,
		sudoUIDSet:  sudoUIDSet,
		lookup:      user.Lookup,
		lookupID:    user.LookupId,
	})
}

func resolveInvoking(source identitySource) (string, bool, error) {
	if source.uid != source.euid {
		return "", false, fmt.Errorf("real and effective UID differ: %d != %d", source.uid, source.euid)
	}
	if source.uid != 0 {
		if source.lookupID == nil {
			return "", false, fmt.Errorf("nil UID lookup")
		}
		uid := strconv.Itoa(source.uid)
		account, err := source.lookupID(uid)
		if err != nil {
			return "", false, fmt.Errorf("look up invoking UID %s: %w", uid, err)
		}
		if err := validateAccount(account, uid, "real UID"); err != nil {
			return "", false, err
		}
		return account.Username, true, nil
	}

	if !source.sudoUserSet && !source.sudoUIDSet {
		return "", false, nil
	}
	if !source.sudoUserSet || !source.sudoUIDSet || source.sudoUser == "" || source.sudoUID == "" {
		return "", false, fmt.Errorf("incomplete sudo identity: both SUDO_USER and SUDO_UID are required")
	}
	uidNumber, err := strconv.ParseUint(source.sudoUID, 10, 32)
	if err != nil {
		return "", false, fmt.Errorf("invalid SUDO_UID %q: %w", source.sudoUID, err)
	}
	uid := strconv.FormatUint(uidNumber, 10)
	if source.lookup == nil {
		return "", false, fmt.Errorf("nil username lookup")
	}
	account, err := source.lookup(source.sudoUser)
	if err != nil {
		return "", false, fmt.Errorf("look up sudo user %q: %w", source.sudoUser, err)
	}
	if err := validateAccount(account, uid, "SUDO_UID"); err != nil {
		return "", false, err
	}
	if uidNumber == 0 {
		return "", false, nil
	}
	return account.Username, true, nil
}

func validateAccount(account *user.User, expectedUID, source string) error {
	if account == nil {
		return fmt.Errorf("user lookup returned no account")
	}
	if account.Username == "" {
		return fmt.Errorf("user lookup returned an empty username")
	}
	uidNumber, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid account UID %q: %w", account.Uid, err)
	}
	uid := strconv.FormatUint(uidNumber, 10)
	if uid != expectedUID {
		return fmt.Errorf("user %q UID %s does not match %s %s", account.Username, uid, source, expectedUID)
	}
	return nil
}
