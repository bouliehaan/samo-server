# Local integration check

With the updated `samo-explo` checkout beside `samo-server`, start the repository's
disposable PostgreSQL (`make test-db` from the Samo root), then run:

```sh
cd integration/explo
go test -race ./...
```

This separate test module connects the real Samo handlers and Explo service over
localhost, using only existing Samo credentials. It checks automatic registration,
persistence, authenticated routing and disconnect gating. It uses a disposable
database and does not contact deployed services or download music.
