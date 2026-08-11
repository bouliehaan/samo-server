package api

import (
	"context"
	"errors"

	"github.com/bouliehaan/samo-server/internal/users"
)

// SamoRadioTokenMinter adapts the users service to what internal/samoradio
// needs, so that package never has to know Samo has accounts at all.
//
// Device tokens belong to the reserved `user-server` account rather than to
// whichever admin happened to click PAIR. Two reasons: the device keeps working
// after that admin is deleted or has their tokens rotated, and a device
// credential never shows up in a human's personal token list where it could be
// revoked by someone wondering what it is.
type SamoRadioTokenMinter struct {
	Users *users.Service
}

// ErrUsersDisabled means there is no account system to mint a token from.
var ErrUsersDisabled = errors.New("user accounts are disabled")

func (m SamoRadioTokenMinter) actor() (users.Principal, error) {
	if m.Users == nil || !m.Users.Enabled() {
		return users.Principal{}, ErrUsersDisabled
	}
	return users.Principal{User: users.User{
		ID:       users.BootstrapUserID,
		Username: "server",
		Role:     users.RoleAdmin,
	}}, nil
}

// IssueDeviceToken mints a long-lived API token for a device.
func (m SamoRadioTokenMinter) IssueDeviceToken(ctx context.Context, label string) (string, string, string, error) {
	actor, err := m.actor()
	if err != nil {
		return "", "", "", err
	}
	issued, err := m.Users.IssueToken(ctx, actor, users.CreateTokenInput{Label: label})
	if err != nil {
		return "", "", "", err
	}
	return issued.Token.ID, issued.Secret, actor.User.ID, nil
}

// RevokeDeviceToken invalidates a device's token.
func (m SamoRadioTokenMinter) RevokeDeviceToken(ctx context.Context, userID, tokenID string) error {
	actor, err := m.actor()
	if err != nil {
		return err
	}
	if userID != "" {
		actor.User.ID = userID
	}
	return m.Users.RevokeToken(ctx, actor, tokenID)
}
