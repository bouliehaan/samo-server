package extauth

import "database/sql"

// Service is the settings and both providers, as the API holds them.
type Service struct {
	Store *Store
	OIDC  *OIDC
	LDAP  *LDAP
}

func New(db *sql.DB) *Service {
	return &Service{Store: NewStore(db), OIDC: NewOIDC(), LDAP: NewLDAP()}
}
