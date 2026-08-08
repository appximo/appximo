<!-- The Appximo INSTALL PROMPT (INSTALL-PROMPT-S1). This file IS the prompt: it
     is embedded verbatim into the binary (`appximo prompt --install`) and
     pasted verbatim into coding agents by users. It has ONE job — leave the
     right binary on the machine — and must never grow into build guidance;
     that is docs/MASTER_PROMPT.md, which the user pastes AFTER this one.
     Every line exists because a real starting state needs it: nothing
     installed, an OLD version already on the PATH (the most common state once
     releases exist), or the requested version already present. -->
Install the Appximo engine on this machine, or update the one that is already
here. Do ONLY this — do not build or scaffold anything; I will paste a second
prompt for that once you are done.

WHICH VERSION: latest

(Leave it as `latest`, or replace it with an exact tag like `v0.1.5`.)

## Rules

- Ask me NOTHING. Every decision below has a default; take it.
- Work out my platform yourself (Linux, macOS or Windows) and use the matching
  section. Do not ask me which one I am on.
- Finish by proving the result with the success checklist, not by asserting it.

## Step 1 — what is already here?

```
appximo version
```

Three possible states — identify mine before downloading anything:

- **(a) Not installed** (`command not found` / `not recognized`) → install it.
- **(b) An older version** → update it. This is the common case, and the one
  that silently breaks things: an old binary will not have newer commands, and
  the failure looks like a typo instead of a stale install.
- **(c) Already the version I asked for** → change NOTHING. Say so, run the
  checklist, and stop. Do not reinstall "to be safe".

Also note WHERE it lives (`which appximo` / `where.exe appximo`) — an update
must replace THAT file, or the PATH will keep finding the old one. If you find
more than one copy on the PATH, say so and replace the first one the shell
resolves.

## Step 2 — resolve the version I asked for

If I said `latest`, resolve the real tag without guessing — this redirect names
it and needs no API token or rate limit:

```
curl -sI https://github.com/appximo/appximo/releases/latest | grep -i '^location:'
```

The tag is the last path segment (e.g. `v0.1.5`). Compare it with what Step 1
printed. **Equal → state (c): stop, nothing to do.** Different → continue.

Download URLs, for `latest`, never carry a version (these aliases always point
at the newest release):

```
https://github.com/appximo/appximo/releases/latest/download/appximo-linux-amd64
https://github.com/appximo/appximo/releases/latest/download/appximo-linux-arm64
https://github.com/appximo/appximo/releases/latest/download/appximo-darwin-amd64
https://github.com/appximo/appximo/releases/latest/download/appximo-darwin-arm64
https://github.com/appximo/appximo/releases/latest/download/appximo-windows-amd64.exe
https://github.com/appximo/appximo/releases/latest/download/checksums.txt
```

If I named an exact tag instead, use that release's own versioned assets:
`https://github.com/appximo/appximo/releases/download/<TAG>/appximo-<TAG>-linux-amd64`
(and `checksums.txt` from the same `/download/<TAG>/` directory).

Pick the file for MY platform and CPU (`uname -sm`, or `$env:PROCESSOR_ARCHITECTURE`).

## Step 3 — install or update

### Linux / macOS

```bash
cd "$(mktemp -d)"
curl -fsSLO https://github.com/appximo/appximo/releases/latest/download/appximo-linux-amd64
curl -fsSLO https://github.com/appximo/appximo/releases/latest/download/checksums.txt

# integrity: the checksums file lists BOTH the alias and the versioned name,
# so this works for the alias download too
grep " appximo-linux-amd64$" checksums.txt | sha256sum -c -

chmod +x appximo-linux-amd64
sudo install -m 0755 appximo-linux-amd64 /usr/local/bin/appximo   # replaces an old copy in place
```

(Drop the `sudo` if you are already root or the box has none — say which you
did. Replace the file the shell ALREADY resolves in Step 1, then `hash -r`.)

On macOS the file is `appximo-darwin-arm64` (Apple Silicon) or
`appximo-darwin-amd64` (Intel), and `sha256sum` is `shasum -a 256`. If macOS
quarantines the download, clear it: `xattr -d com.apple.quarantine <file>`.

If `/usr/local/bin` is not writable and there is no `sudo`, install to
`~/.local/bin` instead and make sure that directory is on the PATH — say
explicitly which one you chose.

**Replacing a RUNNING binary is fine here**: `install`/`mv` swaps the file, and
any already-running process keeps the old copy open until it exits.

### Windows (PowerShell)

⚠ **Windows cannot overwrite a binary that is currently running or open.** A
plain copy fails with "being used by another process". Do it in this order:

```powershell
$dir = "$env:LOCALAPPDATA\Appximo"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$tmp = Join-Path $env:TEMP "appximo-new.exe"

Invoke-WebRequest -Uri "https://github.com/appximo/appximo/releases/latest/download/appximo-windows-amd64.exe" -OutFile $tmp
Invoke-WebRequest -Uri "https://github.com/appximo/appximo/releases/latest/download/checksums.txt" -OutFile "$env:TEMP\checksums.txt"

# integrity
$want = (Select-String -Path "$env:TEMP\checksums.txt" -Pattern 'appximo-windows-amd64\.exe$').Line.Split(' ')[0]
$got  = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLower()
if ($want -ne $got) { throw "checksum mismatch — do not install this file" }

$target = Join-Path $dir "appximo.exe"
if (Test-Path $target) {
  # The old exe may be running or open: RENAME it (Windows allows renaming a
  # running executable, it just refuses to overwrite one), then put the new one
  # in its place. Delete the .old file later, or on the next update.
  $old = Join-Path $dir "appximo.old.exe"
  Remove-Item $old -Force -ErrorAction SilentlyContinue
  Rename-Item $target $old -Force
}
Move-Item $tmp $target -Force
```

If the rename ALSO fails, something is holding the file: close every terminal,
editor and running `appximo serve`, then retry. As a last resort reboot — do
not install a second copy somewhere else on the PATH, which is how two
different versions end up shadowing each other.

**PATH**: `$env:LOCALAPPDATA\Appximo` must be on it, permanently:

```powershell
[Environment]::SetEnvironmentVariable(
  "Path", [Environment]::GetEnvironmentVariable("Path","User") + ";$env:LOCALAPPDATA\Appximo", "User")
```

Then **open a NEW terminal** — the current one keeps the old PATH, and every
"it still says command not found" after an install is this.

### Optional: verify the signature, not just the checksum

The checksum only proves the download was not corrupted in transit — it lives
in the same release as the binary. If you have (or can install) `cosign`, this
verifies the release was really produced by the project's CI:

```
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'github.com/appximo/appximo' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Skip it if cosign is not available; do NOT skip the checksum.

## Step 4 — the success checklist (run all three, show me the output)

```
appximo version     # must print the version I asked for — not the old one
appximo prompt      # must print a long prompt starting "You are going to build me…"
appximo --help      # must list: up, new, prompt, serve, validate, migrate, specs
```

- If `version` still shows the OLD number: the shell cached the old path
  (`hash -r` on Linux/macOS) or there is a second copy earlier on the PATH
  (`which -a appximo` / `where.exe appximo`) — fix that, don't reinstall.
- If `prompt` says `unknown command`: the update did not take effect. That
  command is the proof; do not report success without it.

Then tell me: the version now installed, where the binary lives, and whether
this was a fresh install, an update from which version, or a no-op. Stop there
— I will paste the build prompt next.

## If something fails

- **No network / TLS errors** → say so plainly and stop; do not fall back to
  building from source or to an unverified mirror.
- **No write permission at the destination** → name the directory that refused,
  and either use `sudo` (Linux/macOS) or install into a user-writable directory
  that is on the PATH. Never silently install somewhere the PATH will not find.
- **Checksum mismatch** → delete the file and retry once. If it mismatches
  again, stop and tell me; do not install it.
