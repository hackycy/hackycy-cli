package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/filesession"
)

type ServerSession struct {
	Token     string
	Account   ServerAccount
	ExpiresAt string
}

// ServerSessions composes account credentials with the shared fresh-Go
// file-session v1 owner. HTTP cookies and SSE are separate adapters.
type ServerSessions struct {
	accounts *ServerAccounts
	store    *filesession.Manager

	accountObserversMu  sync.Mutex
	accountObservers    map[uint64]func()
	nextAccountObserver uint64
}

func NewServerSessions(accounts *ServerAccounts, store *filesession.Manager) (*ServerSessions, error) {
	if accounts == nil {
		return nil, errors.New("Tunnel server accounts are required")
	}
	if store == nil {
		return nil, errors.New("Tunnel server session store is required")
	}
	return &ServerSessions{
		accounts:         accounts,
		store:            store,
		accountObservers: make(map[uint64]func()),
	}, nil
}

func (sessions *ServerSessions) SignIn(ctx context.Context, username, password string) (ServerSession, error) {
	account, err := sessions.accounts.verifyCredentials(ctx, username, password)
	if err != nil {
		return ServerSession{}, err
	}
	revision, err := sessions.credentialRevision(ctx, account.ID)
	if err != nil {
		return ServerSession{}, err
	}
	grant, err := sessions.store.Issue(account.ID, revision)
	if err != nil {
		return ServerSession{}, mapServerSessionError(err)
	}
	return ServerSession{Token: grant.Token, Account: account, ExpiresAt: formatServerTimestamp(grant.ExpiresAt)}, nil
}

func (sessions *ServerSessions) Resume(ctx context.Context, token string) (*ServerSession, error) {
	grant, err := sessions.store.Resume(token, func(accountID string) string {
		revision, revisionErr := sessions.credentialRevision(ctx, accountID)
		if revisionErr != nil {
			return ""
		}
		return revision
	})
	if err != nil {
		return nil, mapServerSessionError(err)
	}
	if grant == nil {
		return nil, nil
	}
	account, err := sessions.accounts.GetAccount(ctx, grant.Subject)
	if err != nil {
		if revokeErr := sessions.store.Revoke(token); revokeErr != nil {
			return nil, mapServerSessionError(revokeErr)
		}
		return nil, nil
	}
	return &ServerSession{Token: grant.Token, Account: account, ExpiresAt: formatServerTimestamp(grant.ExpiresAt)}, nil
}

func (sessions *ServerSessions) SignOut(token string) error {
	if err := sessions.store.Revoke(token); err != nil {
		return mapServerSessionError(err)
	}
	return nil
}

func (sessions *ServerSessions) RevokeAccount(accountID string) error {
	if err := sessions.store.RevokeSubject(accountID); err != nil {
		return mapServerSessionError(err)
	}
	return nil
}

func (sessions *ServerSessions) Observe(token string, listener func()) func() {
	return sessions.store.Observe(token, listener)
}

func (sessions *ServerSessions) observeAccountChanges(listener func()) func() {
	if listener == nil {
		return func() {}
	}
	sessions.accountObserversMu.Lock()
	id := sessions.nextAccountObserver
	sessions.nextAccountObserver++
	sessions.accountObservers[id] = listener
	sessions.accountObserversMu.Unlock()
	return func() {
		sessions.accountObserversMu.Lock()
		delete(sessions.accountObservers, id)
		sessions.accountObserversMu.Unlock()
	}
}

func (sessions *ServerSessions) notifyAccountChanged() {
	sessions.accountObserversMu.Lock()
	observers := make([]func(), 0, len(sessions.accountObservers))
	for _, observer := range sessions.accountObservers {
		observers = append(observers, observer)
	}
	sessions.accountObserversMu.Unlock()
	for _, observer := range observers {
		observer()
	}
}

func (sessions *ServerSessions) ChangeLocalAccountRole(ctx context.Context, accountID string, role AccountRole) (ServerAccount, error) {
	account, changed, err := sessions.accounts.ChangeLocalAccountRole(ctx, accountID, role)
	if err != nil || !changed {
		return account, err
	}
	if err := sessions.RevokeAccount(accountID); err != nil {
		return ServerAccount{}, err
	}
	sessions.notifyAccountChanged()
	return account, nil
}

func (sessions *ServerSessions) ResetLocalAccountPassword(ctx context.Context, accountID, password string) (ServerAccount, error) {
	account, err := sessions.accounts.ResetLocalAccountPassword(ctx, accountID, password)
	if err != nil {
		return ServerAccount{}, err
	}
	if err := sessions.RevokeAccount(accountID); err != nil {
		return ServerAccount{}, err
	}
	sessions.notifyAccountChanged()
	return account, nil
}

func (sessions *ServerSessions) DeleteLocalAccount(ctx context.Context, accountID string) error {
	if err := sessions.accounts.DeleteLocalAccount(ctx, accountID); err != nil {
		return err
	}
	if err := sessions.RevokeAccount(accountID); err != nil {
		return err
	}
	sessions.notifyAccountChanged()
	return nil
}

func (sessions *ServerSessions) ChangeOwnLocalAccountPassword(ctx context.Context, accountID, currentPassword, replacementPassword string) (ServerAccount, error) {
	account, err := sessions.accounts.ChangeOwnLocalAccountPassword(ctx, accountID, currentPassword, replacementPassword)
	if err != nil {
		return ServerAccount{}, err
	}
	if err := sessions.RevokeAccount(accountID); err != nil {
		return ServerAccount{}, err
	}
	return account, nil
}

func (sessions *ServerSessions) credentialRevision(ctx context.Context, accountID string) (string, error) {
	account, credential, err := sessions.accounts.accountCredential(ctx, accountID)
	if err != nil {
		return "", err
	}
	value := strings.Join([]string{account.ID, strings.ToLower(account.Username), string(account.Role), credential}, "\x00")
	revision, err := sessions.store.CredentialRevision(value)
	if err != nil {
		return "", mapServerSessionError(err)
	}
	return revision, nil
}

func mapServerSessionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, filesession.ErrStorageUnavailable) || errors.Is(err, filesession.ErrClosed) || errors.Is(err, filesession.ErrDirectoryInUse) {
		return serverDomainError("SESSION_UNAVAILABLE", "Session storage is unavailable")
	}
	return fmt.Errorf("Tunnel server session operation: %w", err)
}
