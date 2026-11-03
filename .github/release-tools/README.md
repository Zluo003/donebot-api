# Release matrix

The release workflow builds the frontend once, then runs each configured Go target on its own Linux runner. `CGO_ENABLED=0` permits cross-compilation. DoneBot releases target Linux amd64 and arm64, as configured in `.goreleaser.yaml`; simple releases select Linux amd64 only. Archives retain the `sub2api_<version>_linux_<arch>.tar.gz` names for installer compatibility.

Each build uses GoReleaser OSS in snapshot mode with the selected release version and one target. Archive naming, bundled files, Go flags and release templates remain in the existing GoReleaser configurations. Every archive is accompanied by its source commit, target, version and SHA256. The publishing job verifies the complete matrix before building images or publishing. It uses GoReleaser's `extra_files` support to publish existing archives and checksums, with builds disabled. No Pro license is needed.

Go caches are isolated by target and refreshed on each source commit, with fallback to the preceding target cache. Save uses the original restore key, with dependency versions taken from the committed Go module files. Release hooks download modules without running `go mod tidy`. Matrix jobs upload uniquely named artifacts. The publishing job extracts only the regular Linux binary from each verified archive and restores its executable permission before constructing Docker contexts. QEMU remains limited to runtime-image instructions. DockerHub images are omitted when its credentials are absent; GHCR publishes `ghcr.io/zluo003/donebot-api` (derived from the current repository). Simple mode still publishes only the amd64 GHCR image and the simple release description.

All build jobs use the commit resolved by `prepare`, including a manual release's selected tag. Helper scripts come from the workflow revision and are passed as a run-local artifact, so older application tags do not need to contain the new scripts. The workflow serializes release runs to prevent simultaneous updates to moving image tags.

## Validate without publication

From a branch containing this workflow:

```bash
gh workflow run release.yml --ref <branch> \
  -f tag=<branch> -f dry_run=true -f simple_release=false
```

A dry run builds all selected archives and both runtime images, verifies artifact provenance and produces the final checksum file. It exports images locally as OCI archives instead of pushing them. It skips registry logins, GitHub Release publication, DockerHub description updates and VERSION synchronization. Test the simple path separately with `simple_release=true`.

Dry-run artifacts are available in the Actions run, including `release-dry-run-report`. Compare job start/end times, GoReleaser's build duration and cache restore results. Do not present an initial cold-cache run as a warmed-cache benchmark; publishing network time is not measured by dry runs.

Helper checks:

```bash
python -m pip install -r .github/release-tools/requirements-release.txt
python -m unittest discover -s .github/release-tools -p 'test_release_matrix.py'
bash -n .github/release-tools/release-images.sh
```

## Publish DoneBot Linux releases

Push the reviewed application commit to `https://github.com/Zluo003/donebot-api`, then create and push an annotated `vX.Y.Z` tag. The tag workflow builds the embedded frontend and both Linux binaries, verifies hashes and source commits, publishes the GHCR images, and attaches the two archives plus `checksums.txt` to the GitHub Release. The installed executable and systemd service remain named `sub2api`.

```bash
git tag -a v0.3.0 -m "DoneBot API 0.3.0"
git push origin v0.3.0
gh run list --workflow release.yml
gh release view v0.3.0 --repo Zluo003/donebot-api
```
