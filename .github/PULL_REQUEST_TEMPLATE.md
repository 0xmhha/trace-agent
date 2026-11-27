## Summary

<!-- What does this change do, and why is it needed? Link the issue if there is one, for example "Closes #123". -->

## Changes

-

## How it was tested

<!-- Commands you ran and anything you checked by hand. -->

## Checklist

- [ ] `go vet ./...` and `go test -race -count=1 ./...` pass in the root module
- [ ] `npm run build` passes in `desktop/frontend` and `go test ./...` passes in `desktop/`
- [ ] `gofmt -l .` prints nothing and `golangci-lint run ./...` is clean in both modules
- [ ] New behaviour has tests, including failure paths
- [ ] User-visible changes are documented (`README.md`, `README-ko.md`, `docs/`) and listed under `Unreleased` in `CHANGELOG.md`
- [ ] No real transcript content, personal paths, e-mail addresses or secrets were added
- [ ] Commit messages follow Conventional Commits
