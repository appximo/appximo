window.BENCHMARK_DATA = {
  "lastUpdate": 1788151915544,
  "repoUrl": "https://github.com/appximo/appximo",
  "entries": {
    "Benchmark": [
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "bb6400808d63204993b7a6f425707017f7495bd9",
          "message": "fix(site): doc links absolute to the GitHub blob — ../docs/ breaks on GitHub Pages\n\nThe page is about to be published from the gh-pages branch root, where\nthe repo's docs/ directory does not exist; relative links 404ed there.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T17:20:11Z",
          "tree_id": "cac32dfa78852cce1ba7c7467590bf76007274f7",
          "url": "https://github.com/appximo/appximo/commit/bb6400808d63204993b7a6f425707017f7495bd9"
        },
        "date": 1785950505955,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5597,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "419356 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5597,
            "unit": "ns/op",
            "extra": "419356 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "419356 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "419356 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 55.39,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "43332336 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 55.39,
            "unit": "ns/op",
            "extra": "43332336 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "43332336 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "43332336 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "3eff531b89c85f515f8162b23514429ddf922e17",
          "message": "docs(backlog): HOUSEKEEPING-S1 recorded — SCHEMA-6 and SEC-6 DONE, OPS-17 reduced to the DNS half, site live on Pages\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T17:25:30Z",
          "tree_id": "4936b3e4e057407b00b998a87bae63b6ee93c8e1",
          "url": "https://github.com/appximo/appximo/commit/3eff531b89c85f515f8162b23514429ddf922e17"
        },
        "date": 1785950971023,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6252,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "376314 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6252,
            "unit": "ns/op",
            "extra": "376314 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "376314 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "376314 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 70.1,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "34469985 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 70.1,
            "unit": "ns/op",
            "extra": "34469985 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "34469985 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "34469985 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "539afdba75dd1c7eba660a05dd2c00c3dd12ce40",
          "message": "fix(ci): the perf job's JWT_SECRET meets the SEC-6 floor — 17 chars refused to boot\n\nThe floor this session enforced (engine refuses JWT_SECRET under 32\ncharacters) caught the CI perf job's own literal secret: the engine\nexited at boot and the k6 gate failed on both post-floor pushes. The\nclass 'a stated rule the engine now enforces' includes our own CI env.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T17:38:53Z",
          "tree_id": "78d7b8f123bac61f401987d3a35d8dfb2474a127",
          "url": "https://github.com/appximo/appximo/commit/539afdba75dd1c7eba660a05dd2c00c3dd12ce40"
        },
        "date": 1785951565557,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4511,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "519145 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4511,
            "unit": "ns/op",
            "extra": "519145 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "519145 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "519145 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 50.97,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "47491208 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 50.97,
            "unit": "ns/op",
            "extra": "47491208 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "47491208 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "47491208 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "cc71b1acee4c2528cfa9d34901bf7923ab04108d",
          "message": "docs: demo links move to the live appximo.com domains — OPS-17 closed\n\nMiguel created the A records (direct, no proxy) and deleted the old\n*.appitools.com ones. Both new domains serve with fresh Let's Encrypt\ncerts. The planned 301s were dropped deliberately: with the old DNS gone\nthose hostnames are unreachable, so redirect blocks would only make\nCaddy retry ACME for unresolvable names — the old site blocks were\nremoved instead.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T20:10:08Z",
          "tree_id": "0c0a88d40ac3d1446aaabc92c3f3631d053fba35",
          "url": "https://github.com/appximo/appximo/commit/cc71b1acee4c2528cfa9d34901bf7923ab04108d"
        },
        "date": 1785960638733,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6223,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "382528 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6223,
            "unit": "ns/op",
            "extra": "382528 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "382528 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "382528 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 67.1,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35922075 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 67.1,
            "unit": "ns/op",
            "extra": "35922075 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35922075 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35922075 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "e91bfdb51129c64b797bdc24b8ea1414c925366f",
          "message": "docs(readme): release badge live — v0.1.1 exists\n\nThe badge was parked behind a comment until the first tagged release;\nv0.1.1 is out (5 assets) and shields.io already renders it. URL verified\nagainst appximo/appximo.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T20:26:45Z",
          "tree_id": "a58453bb3ddd300e0977beb1370fdcd9ee891eeb",
          "url": "https://github.com/appximo/appximo/commit/e91bfdb51129c64b797bdc24b8ea1414c925366f"
        },
        "date": 1785961644359,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5841,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "399582 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5841,
            "unit": "ns/op",
            "extra": "399582 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "399582 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "399582 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.15,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36814689 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.15,
            "unit": "ns/op",
            "extra": "36814689 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36814689 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36814689 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "7f1dde8e41ae29fea33ab51527e0b6f61ab2bd27",
          "message": "docs: v0.1.1 exists — the availability claims catch up with reality\n\nEvery claim verified live before writing: the release badge renders\nv0.1.1; the linux-amd64 asset downloads, its checksum matches\nchecksums.txt and the binary prints 'appximo v0.1.1'; go get\ngithub.com/appximo/appximo@v0.1.1 resolves from the public proxy in a\nscratch module. So: install.sh RELEASE_VERSION=v0.1.1 (the no-binary\ndownload path is live — dry-run passes the gate), GUIDE availability\nnotes flipped, backend-spec §3.0 says the module IS published (the\ncheckout+replace recipe stays as the unreleased-tree workflow), the\nsite status line says public/released, and the two backlog rows\n(release tag, module publication) move to RESOLVED.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T20:30:26Z",
          "tree_id": "b45cf6815f376173f94e56b0130f64d020fda17e",
          "url": "https://github.com/appximo/appximo/commit/7f1dde8e41ae29fea33ab51527e0b6f61ab2bd27"
        },
        "date": 1785961862999,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6186,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "383217 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6186,
            "unit": "ns/op",
            "extra": "383217 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "383217 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "383217 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.97,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36698378 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.97,
            "unit": "ns/op",
            "extra": "36698378 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36698378 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36698378 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "ef14476e265ae11048d3247db6cf54e6578b868b",
          "message": "docs: QUICKSTART.md — the two-track first mile, executed before written\n\nThe condensed path from nothing to a live API, with BOTH tracks side by side\nfor every step: manual (the ground truth — every command was executed against\na real engine, most of them against the v0.1.1 RELEASE binary, before being\nwritten down) and with an AI agent (what to paste, what to ask — the specs\ntrilogy as the contract). Covers install (Linux/macOS verified; Windows\nwritten in PowerShell and marked NOT YET VERIFIED — no Windows machine here,\ntracked as OPS-20), the three settings, schema + validate + the explain\nread-back, serve + first tenant + first calls, the first user (/admin\nbootstrap + signup switch), the custom-Go 10%, the frontend, production with\nHTTPS via install.sh, migrate + Studio, and backup. Four real Playwright\nscreenshots (no mockups); per-step 'you should see' / 'if it fails' rows\nwired to the errors the first-mile pass just made actionable; honest\n'(next release)' markers for every post-v0.1.1 behavior.\n\nCold-read by a second agent against the source: 10 findings (installer flag\nsyntax, a nonexistent restore.sh, the backup timer the installer does NOT\ncreate, four behaviors mislabeled as v0.1.1, two wrong GUIDE chapter refs, a\nmislabeled tenant error, a stale bind-order note, an unbootable Docker\none-liner, a low-entropy PowerShell secret recipe, a superseded downtime\nnumber) — all fixed before landing.\n\nREADME quick start and GUIDE link it; BACKLOG records the session (first-mile\nDONE table, COMMERCE-1/2 → DONE, new OPS-20 and COMMERCE-7).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T21:49:09Z",
          "tree_id": "419b380b753d6ea851129e9383cceb270f4a03ba",
          "url": "https://github.com/appximo/appximo/commit/ef14476e265ae11048d3247db6cf54e6578b868b"
        },
        "date": 1785966583722,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6122,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "370950 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6122,
            "unit": "ns/op",
            "extra": "370950 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "370950 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "370950 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.9,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37154809 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.9,
            "unit": "ns/op",
            "extra": "37154809 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37154809 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37154809 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "f7911af284a13d8d4483bc25393ed7b57861b79f",
          "message": "fix(ci): the integration-tagged metrics test asserts the ENGLISH HELP strings\n\nThe English-first pass translated the Prometheus HELP texts; the one consumer\nthat pins them verbatim lives behind '-tags integration', which the local full\nlane (no tags) never compiles — CI's dedicated step caught it. Lesson repeated\nfrom the SEC-6 k6 incident: a sweep's fixture check must include the TAGGED\ntests and the workflows, not just the untagged lane.\n\nAlso: QUICKSTART step 4 now names --control-port (default 9090) — the ONE real\nfriction the fresh-agent verification hit (it needed a non-default control port\nand the flag was only discoverable in the README's config table).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-05T22:00:48Z",
          "tree_id": "2c41ecdde9def7756e258d89a6d2d8ccf1e52701",
          "url": "https://github.com/appximo/appximo/commit/f7911af284a13d8d4483bc25393ed7b57861b79f"
        },
        "date": 1785967276036,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6497,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "374972 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6497,
            "unit": "ns/op",
            "extra": "374972 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "374972 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "374972 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 71.15,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "34034401 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 71.15,
            "unit": "ns/op",
            "extra": "34034401 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "34034401 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "34034401 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "1701b5a7445e89ebb94a1338cc3564e3b69ae6e2",
          "message": "chore(bench): FIELD-FEEDBACK-S1 ABBA record — M1 read-path change measures no_change\n\n4 arms (base f7911af vs HEAD, A-B-B-A, 6×30s@100rps each on the canonical\nblank/benchblank fixture): median p50 base 0.641 ms vs new 0.585 ms,\nΔ −0.056 ms (−8.8%, new FASTER — within the box's 8.7–10.4% between-run CV\nfloor), MWU p=0.094; the max(0.5ms, 3%) gate passes. Also removed 4 bogus\nffs1-* rows a broken first attempt imported from stale k6 files (the runs\nnever executed — k6 rejected a unitless duration).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-07T04:20:52Z",
          "tree_id": "5360a8d2cfa1fb154b2de7b7bb0a213945f80f04",
          "url": "https://github.com/appximo/appximo/commit/1701b5a7445e89ebb94a1338cc3564e3b69ae6e2"
        },
        "date": 1786076488460,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6236,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "377318 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6236,
            "unit": "ns/op",
            "extra": "377318 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "377318 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "377318 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.47,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37165990 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.47,
            "unit": "ns/op",
            "extra": "37165990 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37165990 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37165990 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "1c9a73f8ecc444e8d9560987c048c85b5ee7e2ac",
          "message": "fix(docker): re-include the two new embedded spec docs in the build context\n\nThe exact CI-GREEN-S1 trap repeating: docs/ is excluded from the Docker\ncontext with per-file re-includes for every //go:embed'd markdown, and the\nsession added two (BACKOFFICE_SPEC_LLM.md, LIFECYCLE_SPEC_LLM.md) without\nextending the list — Docker Publish failed on 'no matching files found'.\nReproduced and fixed locally (full image build green).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-07T04:49:43Z",
          "tree_id": "8de3a94c5b76dc07612c2cc5346c23af3a8239dc",
          "url": "https://github.com/appximo/appximo/commit/1c9a73f8ecc444e8d9560987c048c85b5ee7e2ac"
        },
        "date": 1786078205474,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6275,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "382957 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6275,
            "unit": "ns/op",
            "extra": "382957 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "382957 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "382957 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.61,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37211053 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.61,
            "unit": "ns/op",
            "extra": "37211053 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37211053 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37211053 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "01fa0f60242ec84916cee2df116b6115de7d7e3e",
          "message": "docs: the first ten minutes — QUICKSTART rewritten on up/new, with the measured number (ENG-38)\n\nQUICKSTART.md's first act is now `appximo up` (the card, the /app\nscreenshots, the executable success checklist, the paste-ready agent\nprompt, the ten-minute script) — and the manual path is PRESERVED verbatim\nas §4, explicitly framed as the ground truth and the net: when `up` fails,\nthese are the pieces to diagnose. The published timing is the measured one,\nnot an estimate: a fresh agent holding only this document and the binary\nwent from first command to checklist-green in 1m53s (up: 12s, Postgres\nimage cached; a human should budget ~5 minutes warm).\n\nThe rest of the public surface catches up: README (the one-command lead),\nGUIDE ch.2 (the short way, above the piece-by-piece truth), the site's\n\"How it starts\", `appximo quickstart` (§0-bis: up as the local composition\nof steps 1–4; production never uses it), `backoffice-spec` (/app is the\nbuilt-in embodiment; the `default`-keyword rule joins form rule 1),\nAGENTS.md (the new subcommands + pkg/backofficeui), and the canonical\nstarter snippet aligned in all three copies (README/AGENTS/example — the\nfresh agent caught the doc's own filter example assuming a default the\nstarter didn't declare).\n\nBACKLOG: ENG-38 → DONE with its verification; ENG-39 filed\n(--embedded-pg, deliberately deferred: a runtime-download dependency is\nthe ENG-37 weight class and deserves its own measured session).\nFIELD_FEEDBACK_RESPONSE §13 records the proposal as built, one session\nafter it was filed. benchmarks/history.tsv carries the four ABBA arms\n(skipJWT +1 prefix: all four comparisons no_change; the base-vs-base\ncontrol's −4.2% bounds today's host noise above both treatment deltas).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-07T07:34:10Z",
          "tree_id": "46c0e81c26e276b24d43f157b7aad32b962913dd",
          "url": "https://github.com/appximo/appximo/commit/01fa0f60242ec84916cee2df116b6115de7d7e3e"
        },
        "date": 1786088210463,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6130,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "387963 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6130,
            "unit": "ns/op",
            "extra": "387963 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "387963 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "387963 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.55,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37215459 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.55,
            "unit": "ns/op",
            "extra": "37215459 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37215459 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37215459 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "9b8e03d3afee835a3253c8a1128473dcebe4f719",
          "message": "docs: finding-by-finding response to the second field evaluation (PUBLIC-SURFACE-S1)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-07T21:13:44Z",
          "tree_id": "e3d9c87b58d9ff00bf5da9cd1f84d2a4a8430647",
          "url": "https://github.com/appximo/appximo/commit/9b8e03d3afee835a3253c8a1128473dcebe4f719"
        },
        "date": 1786137263545,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5838,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "407773 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5838,
            "unit": "ns/op",
            "extra": "407773 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "407773 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "407773 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.01,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37726396 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.01,
            "unit": "ns/op",
            "extra": "37726396 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37726396 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37726396 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "7afc8634d341be0bd674e675f24f241b4146d5aa",
          "message": "test: two tagged suites follow BuildQuery's new allowlist parameter\n\nThe exact CI-bites-tagged-tests class the project already recorded twice —\nthe local full lane passed because the fix existed uncommitted.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-07T21:18:54Z",
          "tree_id": "21bc4b622ca8c369de553edf11e3ac4ca5923730",
          "url": "https://github.com/appximo/appximo/commit/7afc8634d341be0bd674e675f24f241b4146d5aa"
        },
        "date": 1786137570501,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6335,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "385399 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6335,
            "unit": "ns/op",
            "extra": "385399 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "385399 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "385399 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 67.07,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35916376 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 67.07,
            "unit": "ns/op",
            "extra": "35916376 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35916376 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35916376 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "65fce17058776a2d08467fabed9d70e13f4cdf0c",
          "message": "docs(site): the entry page leads with the prompt, not the architecture\n\nThe page explained what the engine is before telling anyone how to start\n— the opposite of the references Miguel named (frankenphp.dev opens with\none command; api-platform.com with three numbered steps). Appximo has a\nbetter opening move than either: the user doesn't run a command, they\npaste a prompt.\n\n- Hero = the master prompt in a copy-to-clipboard box, with a measured\n  badge (1m53s, the number a fresh agent actually took) and two tabs —\n  'With your AI agent' (the flagship path) and 'By hand' (the truth and\n  the net, three commands).\n- Three numbered steps: paste it · watch your app run · publish it with\n  HTTPS, each with a real screenshot (the /app back-office, a live HTTPS\n  deploy) instead of prose.\n- Architecture moved below the fold, renamed 'Under the hood'.\n- Live demos are now THREE, including crisblogs.appximo.com — built\n  end-to-end by a third party whose agent only had the printed contracts.\n  It is the strongest social proof the project has; the screenshot is a\n  real capture of the live site.\n- The specs block lists all five printables and names 'appximo prompt'\n  first.\n\nBrowser-verified (Playwright) at 390x844 and 1366x900: no horizontal\nscroll, no console errors, tabs and expand/copy working at both sizes.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T00:39:50Z",
          "tree_id": "6023e9da9e8ac7e12cae3da43381be9499101de3",
          "url": "https://github.com/appximo/appximo/commit/65fce17058776a2d08467fabed9d70e13f4cdf0c"
        },
        "date": 1786149623839,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5956,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "393896 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5956,
            "unit": "ns/op",
            "extra": "393896 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "393896 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "393896 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 53.94,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "44532210 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 53.94,
            "unit": "ns/op",
            "extra": "44532210 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "44532210 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "44532210 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "4ee8f7302eb316cf7711a7b5ede3cd68da3f11be",
          "message": "fix(docker): the image build stops dying on each new embedded doc\n\nDocker Publish failed on 65fce17 with the same mechanism that killed it\nthree times before: docs/ is excluded from the build context, a new\n//go:embed docs/MASTER_PROMPT.md landed, and the image build died with\n'pattern docs/MASTER_PROMPT.md: no matching files found' — long after the\nunit lane was green, because nothing in the fast lane knew about the\ncoupling.\n\n- .dockerignore re-includes docs/MASTER_PROMPT.md (the immediate fix).\n- TestDockerignoreKeepsEmbeddedDocs scans every non-test .go file in the\n  module root for //go:embed docs/... and fails the UNIT lane when the\n  path has no matching '!docs/...' re-include, quoting the exact error the\n  image build would print and the line to add. The list is no longer\n  maintained by memory.\n- TestEmbeddedDocsExist names a stale embed path directly.\n\nMutation-checked: commenting the re-include out turns the guard red, and\nrestoring it green. Verified the real mechanism too — a build against the\nactual context now carries MASTER_PROMPT.md into the image.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T00:55:24Z",
          "tree_id": "c178354caa19f65471560c1036d188a8480cd363",
          "url": "https://github.com/appximo/appximo/commit/4ee8f7302eb316cf7711a7b5ede3cd68da3f11be"
        },
        "date": 1786150551367,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5965,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "398308 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5965,
            "unit": "ns/op",
            "extra": "398308 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "398308 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "398308 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.93,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36979029 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.93,
            "unit": "ns/op",
            "extra": "36979029 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36979029 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36979029 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "3e2ba3ec64a7de91faf7ce8e51321c202b7284e1",
          "message": "fix: three frictions the second fresh-agent run surfaced (warning, static shadowing, backup namespacing)\n\nAll three came from an agent building a recipe box from the master prompt\nwith no repo access — it worked around each one itself, which is exactly\nthe evidence that the product, not the agent, was wrong.\n\n1. SCHEMA-5 warning 'required_field_is_rbac_forced'. An ownership column\n   that is BOTH `required` and the target of an identity row condition\n   fails EVERY create by that role: the declarative required check runs\n   before EnforceCreateRBAC injects the caller's id, so the 422 blames the\n   client for a value it was never meant to send. Legal (another,\n   unscoped role may genuinely have to supply it), almost always a\n   mistake, invisible until the first create — the SCHEMA-5 bar. Verified\n   end to end: `appximo validate` prints it on the exact shape the agent\n   hit.\n\n2. /app.js no longer 404s. isEngineOwnedPath applied the dotted rule\n   (which exists for /openapi.json and /openapi.yaml) to EVERY reserved\n   prefix, so a SPA bundle named app.js was shadowed while style.css from\n   the same mount served — with nothing saying why. The dot rule now\n   belongs to /openapi alone; the engine serves nothing at /app.js,\n   /api.js or /health.css, so those paths go to the mount that does.\n\n3. backup.sh namespaces per app even when invoked with just its env file.\n   The installed copy at /opt/<app>/scripts is naturally called with\n   --env-file=/etc/<app>/<app>.env, and every app was writing\n   'appximo-<stamp>.dump' into one shared directory. It now infers the app\n   from that path (--app still wins), and the rotation glob matches what\n   the run writes — hardcoding 'appximo-*' meant a namespaced app's dumps\n   were never pruned. The installer summary also prints the real database\n   name instead of a hardcoded 'appximo'.\n\nGates: full lane (unit + integration + e2e + resilience, no -short) exit\n0, root tagged suite ok, lint 0 issues, binary-diff gate 117 cases =\n116 SAME + the one expected DIFF (openapi-served-contract, the aggregate\npaths of the previous commit).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T01:10:39Z",
          "tree_id": "a96a9c6d43b755bd5f7b86cb4c8daf722b647c56",
          "url": "https://github.com/appximo/appximo/commit/3e2ba3ec64a7de91faf7ce8e51321c202b7284e1"
        },
        "date": 1786151462974,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6145,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "388230 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6145,
            "unit": "ns/op",
            "extra": "388230 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "388230 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "388230 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.46,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36234626 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.46,
            "unit": "ns/op",
            "extra": "36234626 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36234626 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36234626 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "8c9e9b3c4b6fccb0ac0fc6d8b7df6d027ab714b5",
          "message": "docs(backlog): LAUNCHPAD-S1 review — UI-2 and ENG-40 closed, three items filed\n\nUI-2 was exactly the data-loss it was filed as: Studio's serializer dropped\nrbac.public on every export and deploy. Both it and ENG-40 move to DONE with\ntheir verification recorded. New OPEN: OPS-23 (install.sh has no --static),\nENG-41 (required validated before the RBAC ownership injection — warned now,\nthe reorder deferred with its side effect written down), OPS-24 (the released\nbinaries predate 'appximo prompt', which the website now leads with).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T01:17:21Z",
          "tree_id": "fb55c541eb3816313771528ddc8515bdac53142a",
          "url": "https://github.com/appximo/appximo/commit/8c9e9b3c4b6fccb0ac0fc6d8b7df6d027ab714b5"
        },
        "date": 1786151872898,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6118,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "370590 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6118,
            "unit": "ns/op",
            "extra": "370590 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "370590 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "370590 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.96,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36267073 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.96,
            "unit": "ns/op",
            "extra": "36267073 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36267073 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36267073 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "9d2cb723bc3190d5ed2dca9a3e444710c2991140",
          "message": "docs(site): two numbered prompt steps, and prompt boxes that are not a wall of text\n\nThe entry page led with ONE prompt as plain text. Two problems, both raised\nafter real use: the onboarding is now TWO prompts (install, then build) and\nthe page did not say so; and the prompt itself read as a wall — no\ndistinction between prose and commands, and nothing marking what the reader\nhas to replace.\n\n- Step 1 'Install Appximo' and Step 2 'Build your app' as numbered blocks\n  with a divider between them, so it reads as two moments. Step 1 carries\n  FrankenPHP-style Linux/macOS · Windows tabs previewing the platform\n  commands; Step 2 keeps the agent · by-hand tabs (its by-hand path now\n  starts at 'appximo up', since installing moved to step 1).\n- The prompt markdown is rendered, not dumped: its own headings become\n  visible section rules, fenced blocks become dark inset panels with light\n  shell/PowerShell highlighting, inline backticks become code chips,\n  checklists become ☐ rows.\n- What the reader must replace ('WHICH VERSION: latest', 'MY IDEA: …') is an\n  amber dashed block with a '✏ REPLACE THIS LINE' pill and the placeholder\n  itself highlighted — unmissable instead of buried in a paragraph.\n- A copy button per box. The copied text comes from a hidden raw carrier\n  holding the exact .md body, never from the decorated DOM, so decoration can\n  never drift from the paste; verified by reading the clipboard back in a\n  real browser and asserting full string equality (7696 and 12378 chars) and\n  the absence of HTML tags.\n- The status strip drops the stale 'merged after v0.1.2' caveat: v0.1.5\n  (2026-08-08) ships everything this page describes.\n\nBrowser-verified at 390x844 and 1366x900: 66 checks, 0 failures — no\nhorizontal scroll before or after scrolling, zero console/page errors, both\ncopy buttons copying the exact prompt, both tab groups switching, both boxes\nexpanding and collapsing, all 8 images loaded.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T03:32:14Z",
          "tree_id": "c0815645ff0b2a4e70fba93941affd2bbd3a3afa",
          "url": "https://github.com/appximo/appximo/commit/9d2cb723bc3190d5ed2dca9a3e444710c2991140"
        },
        "date": 1786160342096,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6345,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "405208 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6345,
            "unit": "ns/op",
            "extra": "405208 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "405208 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "405208 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.44,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37163304 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.44,
            "unit": "ns/op",
            "extra": "37163304 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37163304 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37163304 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "05d4c2cb44a9b596ca1c4ebb3be3069d1580e278",
          "message": "fix(prompts): the three leaks the chained fresh-agent run found\n\nA fresh agent was given a container with a STALE v0.1.2 on the PATH and then\nthe two prompts in order. It updated to v0.1.5 in 41 s and had a verified\ndentist-office app running 7 minutes later — but its DOUBTS log names three\nplaces where it had to decide alone. Each is now a line:\n\n- The install prompt's Linux block hardcoded 'sudo', which does not exist on a\n  root-only box; the agent dropped it silently. It now says to drop it when\n  already root, and to replace the file the shell ALREADY resolves.\n- The master prompt had no guidance for a Postgres string that is unreachable\n  for an ENVIRONMENTAL reason. The agent was caught between 'ask nothing more'\n  and 'fix what the error names' — and the error came from the network, where\n  the engine's actionable errors cannot help. It now says: do not silently\n  change the string I gave you, fix reachability at your end and say what you\n  changed in one line, or stop and name the address that did not answer.\n- The rbac.public checklist row was conditional, so a schema without a public\n  block leaves a row that can be quietly skipped. It now demands an explicit\n  N/A plus the proven negative (a tokenless read is 401).\n\nThe site carries the corrected text (re-injected from the same sources, so\npage and docs cannot drift); re-verified in a real browser at both viewports:\nclipboard equals the corrected .md byte for byte (7851 / 13020 chars), the\nthree new lines are visible in the rendered prompts, zero console errors.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T03:42:36Z",
          "tree_id": "dc2171360906703d10d6c695ef6590e6a981909a",
          "url": "https://github.com/appximo/appximo/commit/05d4c2cb44a9b596ca1c4ebb3be3069d1580e278"
        },
        "date": 1786160597539,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5025,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "465747 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5025,
            "unit": "ns/op",
            "extra": "465747 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "465747 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "465747 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 50.74,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "48996780 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 50.74,
            "unit": "ns/op",
            "extra": "48996780 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "48996780 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "48996780 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "c8ca0fd2b5b7c99bc176faa3f6c993b4a1f2d44a",
          "message": "docs(backlog): INSTALL-PROMPT-S1 review — OPS-24 closed by Miguel's v0.1.5, OPS-25 filed\n\nOPS-24 is DONE and not by an agent: Miguel cut v0.1.5 from 8c9e9b3, so the\npublished binaries carry 'appximo prompt' — verified by downloading through\nthe latest alias, checking the sha256, and running it. OPS-25 replaces it as\nthe honest open item: the Windows branch of 'appximo upgrade' is reasoned, not\nexecuted, and its Ready criterion names the four cases someone with a Windows\nbox must run.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-08T03:45:22Z",
          "tree_id": "105e46fd4f8b2d9ec265efbd68183f3111e311a1",
          "url": "https://github.com/appximo/appximo/commit/c8ca0fd2b5b7c99bc176faa3f6c993b4a1f2d44a"
        },
        "date": 1786160748970,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6521,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "388381 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6521,
            "unit": "ns/op",
            "extra": "388381 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "388381 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "388381 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.18,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36387474 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.18,
            "unit": "ns/op",
            "extra": "36387474 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36387474 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36387474 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "0a7c340ec46ecc9880a95cf413d310a2bbe95b7f",
          "message": "fix(ctx): Ctx.Insert/Update really do validate like the generated POST/PATCH\n\nbackend-spec told consumers that Ctx.Insert validates \"exactly like the\ngenerated POST\". It did not, and a third party built 18 resources and 13\ncustom handlers on that promise (VecinGo). What the generated create did and\nthe library create skipped:\n\n  - schema DEFAULTS. Rows written by a custom handler landed with a NULL\n    status, and the NEXT transition then failed with `invalid transition from\n    \"\"` — the row was not merely missing a value, it was outside its own\n    declared lifecycle, with no error at write time.\n  - the create TYPE check (validateCreateTypes).\n  - the state-machine INITIAL states — a custom handler could create a row in\n    a state the schema forbids as an entry point.\n\nClosed the way this project has closed the same class twice before\n(AppendStateTransitionGuard, FieldDef.ReferencedColumn): ONE function both\npaths call, not a patch on the second one. codegen.PrepareCreate applies\ndefaults, then the declarative rules and value types together (so one response\nstill carries every failing field), then the initial states — the generated\nPOST's exact order. PrepareUpdate is its PATCH-semantics counterpart. The\ngenerated handler now calls it too, so a step added there reaches both paths\nby construction.\n\nNumbers, the second reported bug: the declarative rules and the type check\nboth accepted float64 ONLY, on the reasoning that encoding/json produces\nfloat64 — true of the HTTP path, false of the library path, where a handler\npasses what Go computes and what the engine RETURNS from a read is int64 from\nthe driver. Rejecting int64 on write while handing int64 back on read makes\nread-modify-write impossible without a manual cast. schema.AsFloat64 and\nschema.IsIntegral are now the one decision about what a number is, shared by\npkg/schema's rules and codegen's type check; fromJSONBody accepts Go numerics\nas caller input while still excluding time.Time (the engine-injected\ndefault-now value whose rejection once broke every create on such a schema).\n\nAlso: validate --json always emits `warnings` even when empty. With the key\nomitted, \"no warnings\" and \"an engine without the warnings feature\" were the\nsame JSON — a fresh agent reported it could not use zero warnings as a\npositive signal and hand-checked the known rules instead.\n\nTHE GUARANTEE: ctx_parity_integration_test.go runs the SAME payload through\nBOTH paths and asserts identical rows, plus a read-write round trip of values\nthe engine itself returned. It failed on all three cases before this change\n(status NULL; \"must be a number\" for int64) and passes now.\n\nGates: unit lane 0 FAIL, binary-diff gate 117/117 SAME (the generated path is\nbyte-identical after the refactor), ABBA write-path bench base vs new\np50 1.114/1.058 vs 1.031/0.954 ms, delta -0.093 ms, under the 0.5 ms gate,\nwith an A-to-A control of 0.056 ms covering over half of it, so no_change. A\nfresh agent with no repo access rebuilt the reported scenario (default plus\nstate machine plus int64 through ctx.Insert) and needed ZERO workarounds.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T04:09:47Z",
          "tree_id": "89d0f712dacd1c6c2719ba6afa0f48d5cee3f9fa",
          "url": "https://github.com/appximo/appximo/commit/0a7c340ec46ecc9880a95cf413d310a2bbe95b7f"
        },
        "date": 1786249111019,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6231,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "373771 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6231,
            "unit": "ns/op",
            "extra": "373771 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "373771 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "373771 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.81,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36204046 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.81,
            "unit": "ns/op",
            "extra": "36204046 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36204046 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36204046 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "3877dd873082a03a5eb851293725432a9c3905e6",
          "message": "fix(ctx): close the create-RBAC divergence nobody reported, and write the parity audit\n\nAuditing the whole Ctx-vs-generated class (not just the two divergences the\nfield report named) surfaced a third one that is security-relevant and that no\nuser had hit yet.\n\nThe generated POST runs EnforceCreateRBAC, which does THREE things: drops\nfields outside the role's allowlist, FORCES the row-condition column to the\ncaller's resolved identity, and REJECTS a body supplying a different value for\nit. Ctx.Insert did only the first. For a role scoped by\nconditions:{field:owner_id, val:$user_id}:\n\n  POST /api/notes  {\"body\":\"mine\"}                        -> 201, owner_id forced\n  ctx.Insert       {\"body\":\"mine\"}                        -> 201, owner_id NULL\n  POST /api/notes  {\"body\":\"x\",\"owner_id\":\"user-mallory\"} -> 403\n  ctx.Insert       {\"body\":\"x\",\"owner_id\":\"user-mallory\"} -> 201, attributed to mallory\n\nA custom route was a way AROUND a rule /api enforces. Ctx.Insert now calls the\nsame EnforceCreateRBAC, at the same point in the sequence — the function\nalready existed and was already shared with the GraphQL create, so this adds\nno implementation, only a caller.\n\ndocs/audits/CTX_PARITY_AUDIT.md records the whole class: 17 behaviours of the\nwrite path, what each side did, the 5 that diverged, the 3 that differ\nDELIBERATELY (hooks, post-commit effects, numeric precision) with the argument\nfor each, and the 2 left open as ENG-42 (error shape: a unique violation and an\nunknown column reach a handler as raw driver errors instead of the engine's\n409/422 vocabulary) and ENG-43 (Ctx resolves the BOOT schema, so a hot-migrated\ncolumn's rules are not compiled for it the way writeSurface compiles them for\nthe generated path).\n\nbackend-spec stops promising parity it does not have: it now names exactly what\nIS shared, what is not, and why. It also says with emphasis what a field\nevaluator learned the hard way — RETURN the engine's write errors verbatim;\n`return err` gives the caller every failing field, and wrapping it in a generic\nsentence throws away the per-field 422 the engine already computed.\n\nGates: unit lane 0 FAIL, lint 0 issues, binary-diff gate 117/117 SAME.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T04:28:40Z",
          "tree_id": "84e1dc61cb8664c8350aa66e4ac2cba3f6b59cd3",
          "url": "https://github.com/appximo/appximo/commit/3877dd873082a03a5eb851293725432a9c3905e6"
        },
        "date": 1786249754574,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6069,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "381546 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6069,
            "unit": "ns/op",
            "extra": "381546 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "381546 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "381546 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.27,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36485445 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.27,
            "unit": "ns/op",
            "extra": "36485445 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36485445 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36485445 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "9c763d8378dec15c7ab9808077b0f68189103959",
          "message": "docs: CTX-PARITY-S1 review — the third field report answered, three items filed\n\nBACKLOG: the session's DONE block (the parity class audited and closed, up\nagainst a remote database, the installer no longer disturbing neighbours) plus\nthree new OPEN items — ENG-42 (write errors reach a custom handler as raw\ndriver errors instead of the engine's 409/422 vocabulary), ENG-43 (Ctx writes\nagainst the boot schema, so a hot-migrated column's rules are not compiled for\nthe library path), OPS-26 (a schema granting a custom route cannot be booted by\nthe stock binary, which splits the first mile from the custom-handler half).\n\nFIELD_FEEDBACK_RESPONSE gains its third section: every VecinGo finding with\nwhat was done and how it was verified, including the divergence the report did\nNOT contain — Ctx.Insert skipped the create-time RBAC, so an owner-scoped role\ncould attribute a row to another principal through a custom route.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T04:31:03Z",
          "tree_id": "2193ea1163d4da8abeed54fb5fb20ba1e6d84177",
          "url": "https://github.com/appximo/appximo/commit/9c763d8378dec15c7ab9808077b0f68189103959"
        },
        "date": 1786249892851,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5812,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "399634 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5812,
            "unit": "ns/op",
            "extra": "399634 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "399634 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "399634 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 64.98,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37045126 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 64.98,
            "unit": "ns/op",
            "extra": "37045126 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37045126 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37045126 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "650798d1ad5797d845d45ab01c045e04aa27e986",
          "message": "docs: CTX-CLOSE-S1 close-out — the parity audit table fully closed, the backlog's four items DONE\n\nBACKLOG: ENG-42, ENG-43, OPS-26 and OPS-25 move to DONE in\nCTX-CLOSE-S1, with the Windows residue (non-admin Program Files,\nantivirus locks) recorded inside the entry. FIELD_FEEDBACK_RESPONSE\ngains the same-day addendum for the three items the VecinGo batch\nfiled. benchmarks/history.tsv carries the ABBA windows\n(ctc-wA1/B1/B2/A2 — verdict no_change).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T06:10:14Z",
          "tree_id": "771dc3231e04e6fd92b8261dde7954a20c67edda",
          "url": "https://github.com/appximo/appximo/commit/650798d1ad5797d845d45ab01c045e04aa27e986"
        },
        "date": 1786256164986,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6199,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "380646 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6199,
            "unit": "ns/op",
            "extra": "380646 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "380646 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "380646 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.93,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35924223 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.93,
            "unit": "ns/op",
            "extra": "35924223 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35924223 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35924223 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "2036bc3708776a04516ed973602c27cb347f9b57",
          "message": "ci: windows gate iteration 1 — keep LF at checkout, upload the test output as the black box\n\nThe runner image sets core.autocrlf=true system-wide, so checkout\nrewrote every LF file to CRLF — the likely breaker of byte-exact\ncomparisons in the unit lane. And a failed Windows run's logs are not\nAPI-readable from the box that watches CI (no token), so the lane's\noutput now ships as an artifact (nightly.link-fetchable), the same\npattern as the Linux gotest-json.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T06:24:42Z",
          "tree_id": "2df69bc1d4a23c51b605fda569872977ef5b4cba",
          "url": "https://github.com/appximo/appximo/commit/2036bc3708776a04516ed973602c27cb347f9b57"
        },
        "date": 1786256709687,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6173,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "371774 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6173,
            "unit": "ns/op",
            "extra": "371774 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "371774 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "371774 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.59,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36361965 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.59,
            "unit": "ns/op",
            "extra": "36361965 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36361965 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36361965 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "4aacd7e56f227eaa52942c45a6e95f1f39748e2a",
          "message": "test: two platform-blind assertions surfaced by the Windows gate's first run\n\nTestEnsureEnvFileMergesWithoutRewriting asserted 0600 permission bits,\nwhich do not exist on Windows (Stat reports 0666; access is ACLs) — the\nUnix contract is now asserted only where it is real.\nTestNotWritableNamesPathAndFix pinned the literal `sudo appximo\nupgrade`, which the OPS-25 fix made platform-dependent — it now expects\neach platform's own privileged command. This is the gate doing its job\non day one: both tests were green on Linux and wrong about Windows.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T06:45:31Z",
          "tree_id": "ff948572a9e1e18246b4e1b5a486e49769d9ec3e",
          "url": "https://github.com/appximo/appximo/commit/4aacd7e56f227eaa52942c45a6e95f1f39748e2a"
        },
        "date": 1786257961251,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6202,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "382575 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6202,
            "unit": "ns/op",
            "extra": "382575 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "382575 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "382575 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.85,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36468693 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.85,
            "unit": "ns/op",
            "extra": "36468693 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36468693 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36468693 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "bc9adadd7ce28685f1102ea8828b6c7dec73e456",
          "message": "ci: windows gate iteration 2 — scenario transcripts travel in the artifact\n\nScenario 2 (upgrade under a running serve) failed its first execution\nwith no readable diagnosis: step logs need a token this box does not\nhave. Every scenario now Notes its progress and the upgrade's full\noutput into win-e2e/scenario.txt, uploaded always together with\nserve.log/serve.err — the same black-box pattern as the unit lane.\nHealthz wait widened to 45 s (first boot self-bootstraps the control\nplane on a cold runner).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T06:58:01Z",
          "tree_id": "fd3951ca74d5961570a5c9feec172f343a8e9903",
          "url": "https://github.com/appximo/appximo/commit/bc9adadd7ce28685f1102ea8828b6c7dec73e456"
        },
        "date": 1786258710607,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6209,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "365866 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6209,
            "unit": "ns/op",
            "extra": "365866 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "365866 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "365866 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.03,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36299840 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.03,
            "unit": "ns/op",
            "extra": "36299840 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36299840 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36299840 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "bf5dde49152db1dfaea7ee0696693a0a9e38248f",
          "message": "ci: windows gate iteration 3 — the pinned release predates `upgrade`; self-replace runs on the dev build\n\nThe black box named it exactly: scenario 1's freshly installed v0.1.5\nanswered `unknown command \"upgrade\"` — the command shipped after that\ntag. The rename-aside dance is a property of the CODE UNDER TEST, so\nscenarios 2–4 now self-replace the CI-built dev binary (which has the\ncommand); \"the released .exe boots and serves on Windows\" — proven\nincidentally by the failing run's serve.log — becomes its own step.\nOnce a release ships with `upgrade`, bump UPGRADE_TAG and the chain can\ngo release→release.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T07:12:05Z",
          "tree_id": "e6cdc63ddb771931cb06a6556adc45d3233f1ada",
          "url": "https://github.com/appximo/appximo/commit/bf5dde49152db1dfaea7ee0696693a0a9e38248f"
        },
        "date": 1786259552114,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6293,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "378147 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6293,
            "unit": "ns/op",
            "extra": "378147 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "378147 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "378147 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.97,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36217334 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.97,
            "unit": "ns/op",
            "extra": "36217334 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36217334 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36217334 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "5147bd27f2c446a6ee9b3641552b4986da7df082",
          "message": "ci: windows gate iteration 4 — the gate corrected the reasoning: a running .old.exe does NOT block the upgrade\n\nScenario 3's first execution succeeded where the code's comments\npredicted failure: Go's os.Remove uses POSIX delete semantics on\nWindows 10+/NTFS, so the image of a still-RUNNING old process unlinks\ncleanly and the next upgrade proceeds — the leftover-lock failure mode\nreasoned about in INSTALL-PROMPT-S1 does not exist for that case. The\nscenario is now split: 3a asserts the true behavior (success, old serve\nuntouched), 3b manufactures the genuinely-locked case — a no-sharing\nopen handle, the antivirus/editor class — which is what exercises the\ndocumented loud-failure fallback.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T07:25:36Z",
          "tree_id": "8cb76e16e6b17387a76658603e011827e9926bba",
          "url": "https://github.com/appximo/appximo/commit/5147bd27f2c446a6ee9b3641552b4986da7df082"
        },
        "date": 1786260360586,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4901,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "467442 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4901,
            "unit": "ns/op",
            "extra": "467442 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "467442 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "467442 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 49.74,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "48400071 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 49.74,
            "unit": "ns/op",
            "extra": "48400071 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "48400071 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "48400071 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "f1af468d15b6cbb9211034fb1157541fb324782e",
          "message": "ci: windows gate iteration 5 — the runner kills a step's children; the running-serve chain lives in ONE step\n\nIteration 4's black box showed serve.log ending at scenario 2's own\nprobes: the Windows runner terminates a step's child processes when the\nstep ends, so the serve started in scenario 2 was dead by 3a's assert.\nScenarios 2, 3a and 3b now share one step (serve started, asserted and\nstopped within it, with a finally); the always-on Stop-serve step\nremains as backup.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T07:37:27Z",
          "tree_id": "fa1ee37c1b197a3c92eeb5196dc4dab2fd26fca0",
          "url": "https://github.com/appximo/appximo/commit/f1af468d15b6cbb9211034fb1157541fb324782e"
        },
        "date": 1786261074944,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 7039,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "381170 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 7039,
            "unit": "ns/op",
            "extra": "381170 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "381170 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "381170 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 72.98,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "32873930 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 72.98,
            "unit": "ns/op",
            "extra": "32873930 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "32873930 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "32873930 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "276cc4e7df92c5287cfe8df54ab3bee50bc7c25e",
          "message": "ci: windows gate iteration 6 — the truth with serve genuinely alive: a running .old.exe DOES block, loudly\n\nIteration 5's merged step finally ran scenario 3 with serve actually\nrunning, and the original backlog reasoning stands: Windows denies\nDELETE on an executing image and denies the rename-over, so the next\nupgrade fails with the documented actionable message. (Iteration 4 had\n\"proven\" the opposite — an artifact of the runner killing serve between\nsteps, exactly the self-deception this session's method exists to\ncatch.) s3a now asserts the loud failure with the binary intact AND the\nrunning serve untouched; s3b stops serve first and asserts the same\nfailure for the other lock class (a no-sharing handle — antivirus/\neditor), which also could not coexist with the running image's own\nhandles.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T07:50:57Z",
          "tree_id": "c34488679b99bc5f566e4f6335f872832e2d09ac",
          "url": "https://github.com/appximo/appximo/commit/276cc4e7df92c5287cfe8df54ab3bee50bc7c25e"
        },
        "date": 1786261887429,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6177,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "386541 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6177,
            "unit": "ns/op",
            "extra": "386541 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "386541 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "386541 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.02,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36186242 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.02,
            "unit": "ns/op",
            "extra": "36186242 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36186242 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36186242 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "37edf41fe35f72470bebe096c6523a18f557b8a0",
          "message": "docs: OPS-25's DONE entry records what the gate actually found\n\nSeven iterations to green, each one a real lesson: the sudo lie, two\nplatform-blind tests, a pinned release predating `upgrade`, the runner\nkilling per-step children, and the corrected platform truth — a running\n.old.exe DOES block the next upgrade with the documented loud failure\n(an intermediate run had \"proven\" the opposite; the runner had already\nkilled serve). The entry now matches the shipped workflow.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-09T08:02:25Z",
          "tree_id": "630fc493cf7b5373f45ade50ae7096186455a0c4",
          "url": "https://github.com/appximo/appximo/commit/37edf41fe35f72470bebe096c6523a18f557b8a0"
        },
        "date": 1786262568220,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5834,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "400485 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5834,
            "unit": "ns/op",
            "extra": "400485 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "400485 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "400485 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.06,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "32775060 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.06,
            "unit": "ns/op",
            "extra": "32775060 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "32775060 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "32775060 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "9471157c3df941ec293fcc5ba5373f3192ad7ae1",
          "message": "site: the demo in motion (hero + browser tour) and the case study card\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-17T16:31:34Z",
          "tree_id": "508b3972f1f24bcf232def84f69239d970256276",
          "url": "https://github.com/appximo/appximo/commit/9471157c3df941ec293fcc5ba5373f3192ad7ae1"
        },
        "date": 1786984350841,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5928,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "398176 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5928,
            "unit": "ns/op",
            "extra": "398176 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "398176 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "398176 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 55.65,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "43403996 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 55.65,
            "unit": "ns/op",
            "extra": "43403996 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "43403996 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "43403996 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "97480d1e5aaff7360c4aae953b829b6ca93db133",
          "message": "docs: LAUNCH-ASSETS-S1 in the backlog — the DONE block, and ENG-44 (health shares the tenant rate bucket)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-17T17:44:27Z",
          "tree_id": "00ebc05b016721f84fc735f7f133e80720c6e35a",
          "url": "https://github.com/appximo/appximo/commit/97480d1e5aaff7360c4aae953b829b6ca93db133"
        },
        "date": 1786988739832,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6476,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "363231 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6476,
            "unit": "ns/op",
            "extra": "363231 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "363231 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "363231 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.75,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36355611 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.75,
            "unit": "ns/op",
            "extra": "36355611 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36355611 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36355611 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "463bbd45aff981acbfb8e3a93185c9b0bcc6c6f8",
          "message": "site: the recording plays in a real player, and the visual execution tightened\n\nThe hero autoplay video becomes asciinema-player (vendored, no CDN):\ncontrols, seek, speed, copyable text, poster on the running-app card.\nBoth clocks are stated — playback caps dead air at 3 s, the real clock\nis 0:17 / 0:22 / 0:47 — so no number on the page means two things.\n\nPolish, no structural change: one media frame shared by the player and\nthe tour video (title bar + hint + caption) so embeds read as a family;\na poster frame on the tour (it was rendering as a blank white box);\ndemo cards evened out (fixed media height + cover, CTAs on one\nbaseline — the phone screenshot next to two desktop ones made the row\nragged); tabular figures in tables; calmer section rhythm, heading\ntracking, code-block air; the ad-hoc inline styles replaced by classes.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-17T19:42:54Z",
          "tree_id": "d339cc716b3e27e63c8a76d7167ceaaf65c4514e",
          "url": "https://github.com/appximo/appximo/commit/463bbd45aff981acbfb8e3a93185c9b0bcc6c6f8"
        },
        "date": 1786995808163,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4717,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "470127 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4717,
            "unit": "ns/op",
            "extra": "470127 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "470127 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "470127 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 51.04,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "46656118 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 51.04,
            "unit": "ns/op",
            "extra": "46656118 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "46656118 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "46656118 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "b7c4dded741a8a0d9d9b1c41279b01163a6e9ad5",
          "message": "docs: the backlog records the follow-up pass — a pausable hero, and the 0:22 correction it surfaced\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-17T19:50:27Z",
          "tree_id": "c859cedf29e529b63f90f74a24d99a27f8e849c0",
          "url": "https://github.com/appximo/appximo/commit/b7c4dded741a8a0d9d9b1c41279b01163a6e9ad5"
        },
        "date": 1786996262751,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6375,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "350709 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6375,
            "unit": "ns/op",
            "extra": "350709 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "350709 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "350709 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 70.13,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36736513 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 70.13,
            "unit": "ns/op",
            "extra": "36736513 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36736513 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36736513 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "9457fea4ea6779d16da68630e992e962a63d5595",
          "message": "docs: .env.example documents APPXIMO_APP_THEME_CSS / APPXIMO_APP_DEMO_ROLES\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-18T03:32:13Z",
          "tree_id": "2297eb99c1f33246ec204ffcb01dc9777d3a1c15",
          "url": "https://github.com/appximo/appximo/commit/9457fea4ea6779d16da68630e992e962a63d5595"
        },
        "date": 1787031991559,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4785,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "490446 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4785,
            "unit": "ns/op",
            "extra": "490446 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "490446 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "490446 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 51.53,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "45625375 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 51.53,
            "unit": "ns/op",
            "extra": "45625375 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "45625375 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "45625375 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "a91833b34476612d9a35d69a338a415d68178a90",
          "message": "ci(security): the nightly ZAP scan boots its own throwaway engine instead of failing red forever\n\nThe 'no target, no scan' fail-fast was safe by design (S39: an active scan\nmust never point at prod) but left the nightly red every night while scanning\nNOTHING. Now, with no explicit target, the job builds the engine, boots it\nagainst a scratch Postgres service, registers a scratch tenant and scans that:\nthe ZAP action runs docker with --network=host, and scan.localtest.me resolves\nto 127.0.0.1, so the engine gets a real tenant Host. An explicit target\n(ZAP_TARGET_URL / dispatch input) still overrides, still refused if it looks\nlike production. The workflow also triggers on pushes touching itself, so this\nchange is verified green IN Actions rather than waiting for the 03:00 cron.\n\nThrowaway credentials are deliberately visible: the instance lives minutes on\nthe runner loopback; JWT_SECRET satisfies the 32-char floor (SEC-6).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-18T23:12:48Z",
          "tree_id": "8ad7f58d63ddbf4ffa16d045059e9b64915c7fa7",
          "url": "https://github.com/appximo/appximo/commit/a91833b34476612d9a35d69a338a415d68178a90"
        },
        "date": 1787094798176,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5495,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "429727 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5495,
            "unit": "ns/op",
            "extra": "429727 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "429727 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "429727 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 55.9,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "43280407 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 55.9,
            "unit": "ns/op",
            "extra": "43280407 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "43280407 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "43280407 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "eb4c659742085b121b97ba2fb4d6c83042666566",
          "message": "docs: the backlog records SHOWCASE-TRUTH-S1 — the visitability audit's measured fact, OPS-5 DONE, OPS-27 options, new OPS-28 / COMMERCE-8 / COMMERCE-9\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-18T23:17:32Z",
          "tree_id": "aed9319a4cd190792f5b94c95df446a5ab2e5dd0",
          "url": "https://github.com/appximo/appximo/commit/eb4c659742085b121b97ba2fb4d6c83042666566"
        },
        "date": 1787095080945,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4785,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "493669 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4785,
            "unit": "ns/op",
            "extra": "493669 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "493669 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "493669 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 47.82,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "49502380 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 47.82,
            "unit": "ns/op",
            "extra": "49502380 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "49502380 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "49502380 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "da34112ae2d5b79e8b8b76b6c7a7b64e015782d5",
          "message": "docs+site: dead promises swept — crisblogs stays as a story, never a dead link; petfriendly links land on a door that opens\n\nSHOWCASE-TRUTH-S1 measured it: the crisblogs demo buttons answer 401 and the\nbox is not ours (OPS-27, Miguel's call pending), yet README (×2), the site and\nthe case study all linked it — a visible promise that fails at the click, the\nworst possible state. The third-party build STORY is what crisblogs proves,\nand it survives everywhere — without a clickable URL.\n\npetfriendly's README link used to land on a raw 401 JSON; after OPS-28 the\nroot serves a minimal portada (demo panel with published credentials + API\ndocs), so the links now point at the root and say what a visitor can DO.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-19T02:47:56Z",
          "tree_id": "1cd0b2c6c4f9525eb3604ff2fca09da04d58ad64",
          "url": "https://github.com/appximo/appximo/commit/da34112ae2d5b79e8b8b76b6c7a7b64e015782d5"
        },
        "date": 1787107711938,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6349,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "367036 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6349,
            "unit": "ns/op",
            "extra": "367036 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "367036 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "367036 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.59,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36438738 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.59,
            "unit": "ns/op",
            "extra": "36438738 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36438738 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36438738 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "a7689d73828ea241c5b630f57481fcb8b799c55c",
          "message": "docs: the backlog records LINKABLE-TRUTH-S1 — COMMERCE-8/9 and OPS-28 DONE, the swept links noted on OPS-27, new COMMERCE-10\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-19T02:56:04Z",
          "tree_id": "865cce3768de13cc81da028634bc0beb0ab705a6",
          "url": "https://github.com/appximo/appximo/commit/a7689d73828ea241c5b630f57481fcb8b799c55c"
        },
        "date": 1787108194172,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5936,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "363877 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5936,
            "unit": "ns/op",
            "extra": "363877 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "363877 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "363877 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.03,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36613885 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.03,
            "unit": "ns/op",
            "extra": "36613885 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36613885 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36613885 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "0681a7644055624083edda622f8f6af162b70418",
          "message": "docs: the backlog records SHOWHN-MATERIAL-S1 — launch material DONE, new OPS-29\n\nOPS-29: the v0.1.8 tag's Release workflow failed at 'Create GitHub\nRelease' (2026-08-17), so the Releases page and the version-less\ndownload aliases serve v0.1.7 while 'go get @latest' resolves v0.1.8 —\nexactly the inconsistency a launch thread finds in minutes. Re-running\nthe workflow (or cutting the next tag) is the owner's call.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-19T04:23:28Z",
          "tree_id": "46d05d61d22a35f8713bbdb1626d7e0d6fab067b",
          "url": "https://github.com/appximo/appximo/commit/0681a7644055624083edda622f8f6af162b70418"
        },
        "date": 1787113435514,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5983,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "397808 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5983,
            "unit": "ns/op",
            "extra": "397808 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "397808 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "397808 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.85,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36876484 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.85,
            "unit": "ns/op",
            "extra": "36876484 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36876484 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36876484 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "6122b76acbb3c661ee1653f88d4397bef26c23e4",
          "message": "docs+ci: the last claims that wouldn't survive a 'show me' — counted evaluations, no unmeasured prices, an idempotent release step\n\nThe evaluator claim now says exactly what the linkable material shows.\n'Three outside developers' overstated it: docs/FIELD_FEEDBACK_RESPONSE.md\nitself describes the second evaluation as 'an agent working only from the\ndistributed binary + specs', and nothing linkable proves three distinct\npeople. The claim is now 'three independent field evaluations from\noutside the project … one of them driven end to end by the evaluator's\nAI agent' — in the README, and the case study's crisblogs line now says\nwhat the response doc supports. An agent evaluating the product from the\noutside is on-thesis, not a footnote to hide.\n\nNo VPS price ships without a run behind it: the '$7–16 VPS' (site meta,\nGUIDE) and '$6 VPS' (QUICKSTART, the embedded LIFECYCLE spec) become 'a\ncheap VPS'; BENCHMARKS' closing '$6–16/mo' becomes the RAM spec its own\nmemory data backs (a small 1–2 GB VPS). The measured $16/mo with its\nspec stays everywhere it was. ADR-020's target range and its quote in\nthe certification are dated records and stay as history.\n\nAlso swept: the site's 'current release v0.1.5' note (now 'every release\nfrom v0.1.5 onward' — a phrasing that cannot go stale) and its crisblogs\n'25/25 browser checks' (the published response doc says 24/24).\n\nrelease.yml: the 'Create GitHub Release' step is now idempotent — the\nv0.1.8 failure (OPS-29) died in ~1 s inside the create call, a signature\nmatching either a pre-existing draft or a transient API error; the step\nnow converges onto an existing release (upload --clobber + publish) and\nretries a failed create once, so a re-run can never fail on its own\npartial success. The README's Docker badge sorts by semver, so it shows\nv0.1.8 (which Docker Hub already had) instead of a commit SHA.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-19T05:16:44Z",
          "tree_id": "33b1e6e57d144a9d42f7b278eb88d378486de022",
          "url": "https://github.com/appximo/appximo/commit/6122b76acbb3c661ee1653f88d4397bef26c23e4"
        },
        "date": 1787116629735,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6296,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "397525 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6296,
            "unit": "ns/op",
            "extra": "397525 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "397525 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "397525 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.36,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "33575811 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.36,
            "unit": "ns/op",
            "extra": "33575811 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "33575811 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "33575811 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "b1df20c62bc183498f142d56d084ffe5e67aa1e9",
          "message": "docs: 'our own production workload' said honestly — our live apps, operated through the documented path; the heaviest production build is a third party's\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-19T05:18:18Z",
          "tree_id": "e68080298f74715e1adc5d27358aa4f66adb3210",
          "url": "https://github.com/appximo/appximo/commit/b1df20c62bc183498f142d56d084ffe5e67aa1e9"
        },
        "date": 1787116735134,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6335,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "375907 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6335,
            "unit": "ns/op",
            "extra": "375907 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "375907 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "375907 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 68.51,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36697537 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 68.51,
            "unit": "ns/op",
            "extra": "36697537 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36697537 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36697537 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "2659155225c3a67bc3ac2a6a4901a0ce72679d30",
          "message": "docs+bench: backlog records FRESH-AGENT-GAPS-S1 (DONE + OPS-30 + the ENG-45 audit inventory); corpus + bench artifacts\n\n- BACKLOG: the four gaps DONE, OPS-30 (empty-tenant wildcard token, CLI half\n  shipped / engine half deferred with reason), ENG-45 (the 27-finding\n  implicit-requirement audit inventory, each family to close at load or\n  document).\n- corpus.jsonl: a gate row pinning the ?include= undeclared-relation hint (the\n  one intentional DIFF in the 120 SAME + 1 gate run).\n- history.tsv: the ABBA write-path windows for this session (verdict\n  no_change: dnew-base -0.018 ms / -0.7%, under the 0.5 ms gate, control drift\n  5x larger).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-21T01:12:28Z",
          "tree_id": "1047b4842b9389b76a7fc8b28f80bd24ef0c6d06",
          "url": "https://github.com/appximo/appximo/commit/2659155225c3a67bc3ac2a6a4901a0ce72679d30"
        },
        "date": 1787274775538,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4702,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "504712 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4702,
            "unit": "ns/op",
            "extra": "504712 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "504712 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "504712 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 48.23,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "50485520 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 48.23,
            "unit": "ns/op",
            "extra": "50485520 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "50485520 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "50485520 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "f4d255a0c25ce23f0727f211f3c6c2c573a85ff3",
          "message": "docs(backlog): ENG-45 re-prioritized by damage — silent corruption > non-determinism > loud failure > friction\n\nThe two families SILENT-CORRUPTION-S1 closed move to its DONE section; each\nremaining family carries a written disposition (close at load / shared-path\nfix / document — never magic). New family recorded from the audit: create\naccepts a forged id and forged auto values where PATCH answers 422 read_only\n(the ADR-024 same-input class; also the import-semantics question). OPS-30\nstays deferred untouched (auth hot path, its own session).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-21T04:31:29Z",
          "tree_id": "f643791507e16bd9d557d167010de95c8f94e487",
          "url": "https://github.com/appximo/appximo/commit/f4d255a0c25ce23f0727f211f3c6c2c573a85ff3"
        },
        "date": 1787286796122,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6245,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "380328 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6245,
            "unit": "ns/op",
            "extra": "380328 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "380328 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "380328 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.45,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36468662 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.45,
            "unit": "ns/op",
            "extra": "36468662 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36468662 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36468662 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "bbc9121b3ddc25d90c2c262a08c0e8203be2f426",
          "message": "docs(backlog+guides): ENG-45 family 1 closed; the cut line — last engine session before launch\n\nAGENTS.md and SCHEMA_REFERENCE §2.5 document the import declaration and the\nevery-door read_only contract; backend-spec states the governed rule on\nCtx.Insert/Update (passing a client body through verbatim is safe);\nbackoffice-spec catalogs x-appximo-import (a capability, not a form concern —\ngenerated forms keep governed fields read-only).\n\nBACKLOG: family 1 closed with its evidence; the matrix audit's ONE new find\nrecorded with a disposition and not opened (an owner-scoped role can reassign\nits row's condition column on UPDATE with 200 while create forces it with 403\n— a different enforcement seam, its own increment); Ctx.Update's null-required\ngap re-ranked from silent corruption to loud failure. ENG-45 is the map of\nwhat follows AFTER publishing.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-21T21:49:54Z",
          "tree_id": "1030538b540ce94127b9a89f9d5056b40433586d",
          "url": "https://github.com/appximo/appximo/commit/bbc9121b3ddc25d90c2c262a08c0e8203be2f426"
        },
        "date": 1787349032407,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6347,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "370150 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6347,
            "unit": "ns/op",
            "extra": "370150 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "370150 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "370150 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.18,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35194164 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.18,
            "unit": "ns/op",
            "extra": "35194164 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35194164 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35194164 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "75587ac9c5ffa86149806cef7c7506005b401efa",
          "message": "docs(backlog): FRENTE-COMERCIAL-S1 — indexation, claims, return bars, atina as the fourth evaluation; new OPS-31/32, ENG-46, DOC-3 and four decisions for Miguel",
          "timestamp": "2026-08-26T00:13:47Z",
          "tree_id": "c628aec50ced011270b1d6590c6c51677a57e5f8",
          "url": "https://github.com/appximo/appximo/commit/75587ac9c5ffa86149806cef7c7506005b401efa"
        },
        "date": 1787703255595,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5936,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "399100 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5936,
            "unit": "ns/op",
            "extra": "399100 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "399100 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "399100 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.7,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36681242 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.7,
            "unit": "ns/op",
            "extra": "36681242 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36681242 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36681242 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "acf03f3065807e8dab7b034226616e97b41030f8",
          "message": "site+backlog: the technical site on the design system (wordmark hero, marquee, float/tilt/reveal, lazy loops, bundled Inter) — REDISENO-VISUAL-S1",
          "timestamp": "2026-08-26T01:40:49Z",
          "tree_id": "721fcb80c99aebb42113933913bbf0a6607b71b4",
          "url": "https://github.com/appximo/appximo/commit/acf03f3065807e8dab7b034226616e97b41030f8"
        },
        "date": 1787708478345,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6310,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "377180 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6310,
            "unit": "ns/op",
            "extra": "377180 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "377180 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "377180 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.41,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36629947 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.41,
            "unit": "ns/op",
            "extra": "36629947 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36629947 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36629947 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "3ed696cd78a1c7a4b7e38fd3996dd29f5423ee9c",
          "message": "docs(backlog): REDISENO-VISUAL-S2 — the commercial pages rebuilt with the D1–D6 brand decisions",
          "timestamp": "2026-08-26T03:05:31Z",
          "tree_id": "d492497c6fcc8a4f499301fc6b6762af6b0c118b",
          "url": "https://github.com/appximo/appximo/commit/3ed696cd78a1c7a4b7e38fd3996dd29f5423ee9c"
        },
        "date": 1787713558925,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6297,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "376046 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6297,
            "unit": "ns/op",
            "extra": "376046 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "376046 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "376046 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.87,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35387898 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.87,
            "unit": "ns/op",
            "extra": "35387898 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35387898 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35387898 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "5201c3ff9a919e1d2cf9a0be436519c8cc963752",
          "message": "docs(backlog): HERO-Y-DIRECCION-S1 — the component hero, the team voice, the folded demos, VecinGo's door, the contrast fix, and the VecinGo-authorship decision for Miguel",
          "timestamp": "2026-08-26T04:17:31Z",
          "tree_id": "1bbbe7dbc41e2339fdba7fc404ca13e004af95ea",
          "url": "https://github.com/appximo/appximo/commit/5201c3ff9a919e1d2cf9a0be436519c8cc963752"
        },
        "date": 1787717880944,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5194,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "443733 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5194,
            "unit": "ns/op",
            "extra": "443733 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "443733 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "443733 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 53.35,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "45934261 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 53.35,
            "unit": "ns/op",
            "extra": "45934261 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "45934261 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "45934261 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "fbed1e4e823d1f9d2e88f38a2aad6a45719edb5a",
          "message": "docs(backlog): APP-VITRINA-S1 — the /app on the design system, generic on four schemas, themable from one variable, ENG-46 closed, deployed to both demos with drilled rollback, the video re-recorded",
          "timestamp": "2026-08-26T21:19:22Z",
          "tree_id": "0243d3490588e19afb3cb3027d176da1d3af9210",
          "url": "https://github.com/appximo/appximo/commit/fbed1e4e823d1f9d2e88f38a2aad6a45719edb5a"
        },
        "date": 1787779196773,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6251,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "357868 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6251,
            "unit": "ns/op",
            "extra": "357868 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "357868 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "357868 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 72.01,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36664750 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 72.01,
            "unit": "ns/op",
            "extra": "36664750 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36664750 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36664750 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "f9c6ba4071e20f9908b40ad0b6bed5422e4254fc",
          "message": "docs(backlog): TIENDITA-VITRINA-S1 — el panel visible desde el primer segundo en las dos demos, la tienda sobre el sistema, y tres hallazgos abiertos\n\nDONE: el control de dos modos persistente (0 px de scroll y 0,4-0,5 s para\nenterarse de que hay panel, 1,0-2,3 s para estar dentro; antes el único enlace\nestaba en el pie a 1.466/1.736 px), la tienda portada al sistema con láminas\ntejidas para los productos sin foto admisible, movimiento solo con CSS, el\nmodo demostración probado por las dos vías y dos huecos silenciosos suyos\ncerrados, deploy y rollback ensayados en ambas demos. Motor sin tocar: commerce\nreconstruido contra el MISMO commit que corre en la caja (dec6614).\n\nNuevos OPEN:\n- ENG-47 — el limitador de login no tiene variable de entorno y una demo\n  pública comparte UNA identidad (5/min): el sexto visitante del minuto recibe\n  429. Acotado en la SPA; el motor no se puede aflojar.\n- OPS-33 — un valor de entorno con espacios DEBE ir entre comillas: sin ellas\n  rompía el `. env` de redate-demo.sh y el reset dorado nocturno habría fallado\n  en su próxima corrida. Corregido y probado de punta a punta.\n- COMMERCE-11 — las seis fotos de producto pesan 1,55 MB (una de 676 KB) para\n  mosaicos de ~260 px. No se tocó: arreglarlo exige regenerar el dorado.",
          "timestamp": "2026-08-26T23:48:41Z",
          "tree_id": "ce6e61d5c98500314c63a8137617b0398dc219d8",
          "url": "https://github.com/appximo/appximo/commit/f9c6ba4071e20f9908b40ad0b6bed5422e4254fc"
        },
        "date": 1787791978959,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6254,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "380012 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6254,
            "unit": "ns/op",
            "extra": "380012 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "380012 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "380012 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.95,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36601410 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.95,
            "unit": "ns/op",
            "extra": "36601410 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36601410 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36601410 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "cd27ea03813b3a31ab60e6aa7c1fefcb20ecddb4",
          "message": "docs(backlog): MOTOR-AUTORIZACION-S1 — the write-authorization class audited against HEAD/v0.1.9/v0.1.8 and closed as one policy (ADR-027), ENG-47 closed with the default untouched, ENG-48 + RBAC-2 opened, the v0.1.10 advisory decision for Miguel\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-27T02:30:44Z",
          "tree_id": "2c47991f968f7f365a315061dfda41e630aa49c9",
          "url": "https://github.com/appximo/appximo/commit/cd27ea03813b3a31ab60e6aa7c1fefcb20ecddb4"
        },
        "date": 1787797880413,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6274,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "380163 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6274,
            "unit": "ns/op",
            "extra": "380163 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "380163 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "380163 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 67.35,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36587540 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 67.35,
            "unit": "ns/op",
            "extra": "36587540 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36587540 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36587540 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "8bb3cd41c8a5adb89879ebec8d5c1690a3207193",
          "message": "bench: MOTOR-AUTORIZACION-S1 ABBA on the PATCH protocol — authz-A/B/B2/A2, no_change on all four crossings\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-27T02:31:00Z",
          "tree_id": "0923743a05b9598d24d40d1833549e5adb26e36e",
          "url": "https://github.com/appximo/appximo/commit/8bb3cd41c8a5adb89879ebec8d5c1690a3207193"
        },
        "date": 1787797886937,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4718,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "485360 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4718,
            "unit": "ns/op",
            "extra": "485360 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "485360 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "485360 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 47.63,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "50254878 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 47.63,
            "unit": "ns/op",
            "extra": "50254878 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "50254878 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "50254878 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "5bc6dda77f1b5729583370961b0b8de2106fa75a",
          "message": "docs(audit): the evidence pointer names the internal repo instead of a dangling relative link\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-27T05:12:50Z",
          "tree_id": "a37c3ca62ade795a79137a7ad0197ae6c8f48925",
          "url": "https://github.com/appximo/appximo/commit/5bc6dda77f1b5729583370961b0b8de2106fa75a"
        },
        "date": 1787807600215,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5998,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "388940 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5998,
            "unit": "ns/op",
            "extra": "388940 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "388940 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "388940 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.05,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36900847 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.05,
            "unit": "ns/op",
            "extra": "36900847 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36900847 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36900847 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "cdf4e85e75e8038f97a0e51939a4ed99580f5dac",
          "message": "docs(backlog): MOTOR-TIPO-JSON-S1 — the json type audited against HEAD/v0.1.9/v0.1.8 and decided as a JSON value on every door (ADR-028), ENG-49 closed (the breaker counts only unavailability), ENG-50 opened; ABBA json-patch/json-read no_change on all crossings\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-28T01:36:45Z",
          "tree_id": "84062e0e9d4f418cc115f4386369b0fd40d46657",
          "url": "https://github.com/appximo/appximo/commit/cdf4e85e75e8038f97a0e51939a4ed99580f5dac"
        },
        "date": 1787881035049,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6232,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "381211 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6232,
            "unit": "ns/op",
            "extra": "381211 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "381211 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "381211 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.48,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36576458 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.48,
            "unit": "ns/op",
            "extra": "36576458 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36576458 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36576458 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "bc69fe6bb85a0846b7005a4c15deab449d638979",
          "message": "docs(backlog): APP-PODER-S1 — the /app uses the contract it already had (honest paging + Server-Timing, detail by FKs, JSON editor, views/URL, CSV + batched bulk, relation search); ENG-51 opened; the tracing test's HIT assertion made conditional on X-Cache; ABBA app-patch/app-read no_change ×8\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-28T05:19:22Z",
          "tree_id": "5fde4d7221035ede3c03040f3eaa782452cde7cd",
          "url": "https://github.com/appximo/appximo/commit/bc69fe6bb85a0846b7005a4c15deab449d638979"
        },
        "date": 1787894389807,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6299,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "392666 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6299,
            "unit": "ns/op",
            "extra": "392666 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "392666 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "392666 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.49,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37248816 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.49,
            "unit": "ns/op",
            "extra": "37248816 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37248816 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37248816 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "2902d4fc4c9c24b3556bad2cf0fc4759643713e1",
          "message": "docs(backlog): MIGRACION-CONFIANZA-S1 — the real migration's findings closed by damage order (installer verification, the validator question, canonicalization, the batch door in the contract, the memory guard), MIG-FRONT registered for a product decision, OPS-34 (the ABBA base must be built like the new binary), ABBA attributed and no_change, both demos deployed with rollback drilled\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-28T08:20:55Z",
          "tree_id": "4572b3ab718085894cd3256eab5fdbd1c1b1ce3c",
          "url": "https://github.com/appximo/appximo/commit/2902d4fc4c9c24b3556bad2cf0fc4759643713e1"
        },
        "date": 1787905289011,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6264,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "367886 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6264,
            "unit": "ns/op",
            "extra": "367886 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "367886 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "367886 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 71.95,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "34582324 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 71.95,
            "unit": "ns/op",
            "extra": "34582324 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "34582324 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "34582324 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "c655ce7f1bb3a8add660ba7249d5540680cac2a2",
          "message": "docs(backlog): MOTOR-FIELDS-S1 — MIG-FRONT #5 DONE (?fields= pushed to the SELECT + GraphQL pushdown, ADR-029), SCHEMA-8 registered (default omission as a declaration, not built), the 58 has swap; gate 151+12 explained, ABBA read no_change ×4 (host drift declared), both demos deployed with rollback drilled, bench history\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-28T17:20:23Z",
          "tree_id": "5c96d9f13ac141237e3a3f8536701f137e9d8258",
          "url": "https://github.com/appximo/appximo/commit/c655ce7f1bb3a8add660ba7249d5540680cac2a2"
        },
        "date": 1787937656073,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4036,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "574842 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4036,
            "unit": "ns/op",
            "extra": "574842 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "574842 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "574842 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 41.95,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "57648276 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 41.95,
            "unit": "ns/op",
            "extra": "57648276 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "57648276 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "57648276 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "3030dd8f13c1f673a48b9ce194dd89ea0997b567",
          "message": "docs(site): the technical site and docs synchronized with the released v0.1.13 — the browser tour re-recorded on the current /app (87.6 s, real time, ES+EN subtitles; the 2026-08-17 tour archived), every capture of the old panel / old brand re-taken, the six sessions since APP-VITRINA told where an HN visitor reads them with the known limits written, stale numbers fixed (163-case corpus, ~41 MB image, v0.1.13, is_null exists), the migration report answered point by point in public (FIELD_FEEDBACK_RESPONSE §5) — every claim re-verified with requests against the v0.1.13 binary (DOC-VITRINA-S1)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-28T19:40:27Z",
          "tree_id": "8ae78955b27f1e4fb00fcc239775b40ba57369f7",
          "url": "https://github.com/appximo/appximo/commit/3030dd8f13c1f673a48b9ce194dd89ea0997b567"
        },
        "date": 1787946057455,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6264,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "367471 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6264,
            "unit": "ns/op",
            "extra": "367471 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "367471 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "367471 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 71.06,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36245038 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 71.06,
            "unit": "ns/op",
            "extra": "36245038 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36245038 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36245038 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "29f3ec0b881bc7c9afa374768bb533aa2d5f3718",
          "message": "docs(backlog): CENTINELA-C-S1 — Module C DONE (the collector, the eight provoked verdicts, /admin Resources, the overhead on allocs/op + CPU-seconds + RSS with the p99 as an upper bound), OPS-35 registered (Mann-Whitney does not test the tail), BENCHMARKS §4c + §7; gate 163+3 explained, ABBA read no_change ×4, both demos deployed with rollback drilled and the tiendita's first wall read by its own verdict; four list-fields corpus rows made deterministic (sort=title)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-29T03:33:09Z",
          "tree_id": "4689da5565076960d91cbaa561a21a65f034fef8",
          "url": "https://github.com/appximo/appximo/commit/29f3ec0b881bc7c9afa374768bb533aa2d5f3718"
        },
        "date": 1787974420024,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6224,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "377263 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6224,
            "unit": "ns/op",
            "extra": "377263 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "377263 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "377263 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 71.76,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "34378540 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 71.76,
            "unit": "ns/op",
            "extra": "34378540 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "34378540 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "34378540 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "aa081d692926f8128bef9050c0a10e0955ef5c1a",
          "message": "docs(backlog): ENG-51 — a custom route marks no query span, so the self-monitor's db_bound rule is blind on it (seen live on the tiendita's /api/catalogo) (CENTINELA-C-S1)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-29T04:16:35Z",
          "tree_id": "a93a07454a16f79020a098ff02a29e6b079bb5ad",
          "url": "https://github.com/appximo/appximo/commit/aa081d692926f8128bef9050c0a10e0955ef5c1a"
        },
        "date": 1787977028723,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6074,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "379832 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6074,
            "unit": "ns/op",
            "extra": "379832 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "379832 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "379832 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 71.36,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "38377311 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 71.36,
            "unit": "ns/op",
            "extra": "38377311 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "38377311 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "38377311 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "9fd34f58476d2020a0ae62033d5e28dd96ecceee",
          "message": "chore: ignore the laboratory engine binary (appximo-lab)\n\nCo-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>",
          "timestamp": "2026-08-29T10:16:39Z",
          "tree_id": "102fa7745e6438c878038fe8268975529422d44c",
          "url": "https://github.com/appximo/appximo/commit/9fd34f58476d2020a0ae62033d5e28dd96ecceee"
        },
        "date": 1787998647838,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5097,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "471687 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5097,
            "unit": "ns/op",
            "extra": "471687 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "471687 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "471687 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 50.83,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "47182810 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 50.83,
            "unit": "ns/op",
            "extra": "47182810 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "47182810 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "47182810 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "372dc315d75c02153725df120490e52bc381088d",
          "message": "docs(benchmarks): the endurance run and the finding a single-workload benchmark cannot produce (CAPACIDAD-USL-S1)\n\nTwo additions to §4d, both from measurement.\n\nTHE 4-HOUR SOAK. 3,397,227 requests at 240 rps of reads plus 25 rps of PATCH,\nzero transport errors, zero 5xx, goodput median exactly 240.0 rps. Judged by\nSLOPE, never by a value: the live heap after GC has no trend at all (R² 0.00 —\nsawtooth, not staircase), RSS falls, and the only rising signal is Go's total\nmapped memory at +1.5 MiB/h with the live heap flat, which is arena growth and\nnot retained objects. Latency drift first hour to last: p50 −4.4 %, p99\n−27.1 %. No leak, no degradation.\n\nTHE MIXED-LOAD FINDING. The soak's p90 across slices has a median of 500 ms\nwhile its p50 is 2.7 ms — not a tail, a second mode, present from the first\nslice. One controlled A/B, alternated twice, isolates it: 240 rps of reads\nALONE reads p90 3.50 / 3.55 ms; the same reads plus 25 rps of PATCH read p90\n489.7 / 378.0 ms, with the median untouched (2.51 → 2.56 ms). Twenty-five\nwrites per second multiply the read p90 by ~130×.\n\nEvery other benchmark in this document measures one workload at a time and\ntherefore cannot see it, while a real application is never one workload at a\ntime. What it is NOT was measured rather than assumed: not autovacuum (96.5 %\nHOT updates, dead tuples flat at ~4,000 across 374k updates), not host memory\n(PSI 0.05 %), not the disk (IO PSI 0.83 %), not paging, not a leak. The\nremaining candidate is connection occupancy — a write holds a pool connection\nthrough its whole transaction including the commit fsync, and reads and writes\nshare one 10-connection pool with no separation. Filed as ENG-55 with the\nexperiment that would settle it.\n\nBACKLOG. ENG-54 promoted from suspected to CONFIRMED with the tick signals\nbehind it: at 900 rps the scheduler latency plateaus at 25–60 ms while the\nrule's threshold — 5 % of the request p99 — grows past 130 ms, so the ratio\ncan never reach 1 and `cpu_saturated` cannot fire; the engine reported\n`pool_exhausted` while `cpu_busy_fraction` was 0.91–1.10, i.e. with the CPU\npegged. Recorded with its numbers rather than patched blind: the relative\nfloor exists to keep the lock provocation reading `lock_contention`, so any\nfix has to re-run the eight provocations of CENTINELA-C-S1.\n\nCo-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>",
          "timestamp": "2026-08-29T16:58:26Z",
          "tree_id": "d28978bca4446b495985a44200d2a97c4937d087",
          "url": "https://github.com/appximo/appximo/commit/372dc315d75c02153725df120490e52bc381088d"
        },
        "date": 1788022814300,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 5969,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "391905 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 5969,
            "unit": "ns/op",
            "extra": "391905 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "391905 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "391905 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 67.11,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37393034 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 67.11,
            "unit": "ns/op",
            "extra": "37393034 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37393034 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37393034 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "b65ab4bd11d2c656231ac1878dd47f9847e8776f",
          "message": "feat(lab): the ephemeral capacity laboratory — guarded droplets (refusal before the network, tested), a deterministic two-size dataset, and six commands (LAB-CAPACIDAD-S1)\n\nThe leash lives in the wrapper, not the token: DO scopes are per resource\nTYPE, so tools/lab creates only applab-prefix+tag droplets, refuses any\ndestroy that lacks EITHER (or answers to a production IP) with zero network\ncalls — pinned by tests — caps simultaneous droplets at 4, ships a reaper,\nand every mutating command is a dry-run unless -apply. lab down's verdict\ncomes from a final API re-listing, survives partial failure, and is\nidempotent. The topology separates the generator (dedicated c-4) from the\ntargets (shared s-2vcpu-2gb = the customer number; dedicated c-2 = the\nregression box), all in one private VPC, targets provisioned via install.sh.\nA run in which the generator box exceeded 70 % busy is INVALID and named.\ndocs/BENCHMARKS.md §4e declares this the official measurement procedure,\nreplacing the single-host method; the first live run is OPS-37 (blocked on\nthe scoped token).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-29T18:07:46Z",
          "tree_id": "3b55cd7c05e9f261802455cc778009df7f5ddaad",
          "url": "https://github.com/appximo/appximo/commit/b65ab4bd11d2c656231ac1878dd47f9847e8776f"
        },
        "date": 1788026924598,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 4867,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "484166 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 4867,
            "unit": "ns/op",
            "extra": "484166 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "484166 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "484166 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 48.88,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "50338812 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 48.88,
            "unit": "ns/op",
            "extra": "50338812 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "50338812 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "50338812 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "5bc510a83a312758e029f9a4f68f32bc8b77b88c",
          "message": "feat(lab): first live isolated run — the old ceiling was the instrument; the tipping is the app's, at ~1100/~1600 rps (LAB-CAPACIDAD-S2)\n\nThe re-run of the CAPACIDAD-USL-S1 ladder with the generator on its own\ndedicated box (worst 7 % busy vs the 70 % gate, zero runs invalidated)\nshows every old level served at ms-scale p50 and ZERO tips in 9 runs at\n420 rps — the old bistability was generator contention. The tip itself is\nreal and reproduces at each box's true ceiling (shared s-2vcpu-2gb: clean\nto ~1000, 6/8 tipped at 1100, plateau ~1180; dedicated c-2: clean to 1400,\ntips ~1600) — ENG-52 confirmed with clean numbers, ENG-53 reframed, ENG-55\ncorroborated (on 2 vCPU the 10-conn pool is the first queue). USL R²\n0.800 → 0.918/0.945; the gate still refuses a single-number ceiling at the\nbistable levels, so §4e publishes the range. install.sh passed clean on\nboth fresh Ubuntu 24.04 boxes.\n\nLive fixes to the lab, all tested: the scoped token has NO tag permissions\n(tag:create 403s on droplet create; /v2/tags 403) → untagged creation with\na loud warning, prefix listing over the full droplet list, and the destroy\nguard's second factor degrades to the lab-size fingerprint (prefix alone\nstill never authorizes; OPS-38 restores the tag factor); install.sh takes\nonly --flag=value; up's rollback actually runs on provisioning failure\n(fatal/os.Exit skipped the defer); seed via stdin + ON_ERROR_STOP (postgres\ncannot read /root); down's verification polls past DO's async deletion;\ncapacity's fit conditions read the generator location off the data instead\nof asserting the same-host confound; lab builds inject the git revision.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-29T22:52:10Z",
          "tree_id": "03ccb836bb2d1ef603ad336ff8fab87d2764733f",
          "url": "https://github.com/appximo/appximo/commit/5bc510a83a312758e029f9a4f68f32bc8b77b88c"
        },
        "date": 1788043972035,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6378,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "379064 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6378,
            "unit": "ns/op",
            "extra": "379064 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "379064 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "379064 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 67.36,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36734062 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 67.36,
            "unit": "ns/op",
            "extra": "36734062 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36734062 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36734062 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "fe30b5d4fbe6ee66f01f34dfd5b4fcdb911a01b2",
          "message": "feat(engine): admission control — the engine degrades instead of tipping; the rate-limit default derives from measured capacity (ENG-52+ENG-53, MOTOR-PRODUCCION-S2)\n\nThe cause of the tip, named with lab evidence: unbounded in-flight\nconcurrency — every queued request pays parse/tenant/JWT/RBAC before dying\nlate at the 5s query timeout, so wasted work grows with the backlog and\neats exactly the CPU the admitted work needed; that feedback is why tipped\nruns never recovered. The fix caps in-flight data-plane requests at the\nFRONT of the chain (before the tenant limiter, cache, JWT, pool) and sheds\nthe excess as an immediate 429 + Retry-After: 1. APPXIMO_MAX_INFLIGHT,\nauto = max(32, 4×(GOMAXPROCS + pool)); 0 disables; non-integer refuses to\nboot. Concurrency self-adapts by Little's law to plan and workload — no\nestimator, no per-deploy recalibration; SSE, byte-serving, probes, admin\nsurfaces and OPTIONS are out of scope by design.\n\nMeasured in the isolated laboratory, paired on the same instances at each\nbox's tip: shared s-2vcpu-2gb @4800 rps OFF → goodput 3679, p50 1728 ms,\n79k timeouts; ON → 4405 (+20%), 36 ms, ZERO timeouts, 8/8 runs alike.\nDedicated c-2 @6000: 5194/695ms/26.6k → 5676 (+9%)/23ms/0. Zero false\nrejections at every level with headroom. Frozen ABBA at 300 rps:\nno_change in median (Δp50 −0.001 ms, p=0.806) AND tail (Δp99 +0.01 ms,\npermutation p=0.877).\n\nThe per-tenant rate limit's default is now DERIVED: 350 rps × GOMAXPROCS —\n70% of the measured per-core clean ceiling of the canonical uncached read\non the customer-grade shared box (§4e) — instead of a hand-set 1000\nunrelated to capacity. RATE_LIMIT_RPS still overrides; migration note and\nthe composition of the four load defenses in backend-spec §3.8.\n\nAlso: the ENG-55 130× write-interference premise does not reproduce in the\nclean lab (read p90 ×1.02 beside 25 writes/s; pool-30 falsification arm\nunmoved) — §4d says so at the claim, §4e carries the experiment. The\nbinary-diff gate gains a concurrency shed probe and prints both binaries'\nrate-limit defaults (two expected, self-explaining DIFFs); dead capacity\nfigures corrected across docs/site with their conditions.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-30T02:27:16Z",
          "tree_id": "1ddf3866fe4cb197058214615a6ec0755f3e8c78",
          "url": "https://github.com/appximo/appximo/commit/fe30b5d4fbe6ee66f01f34dfd5b4fcdb911a01b2"
        },
        "date": 1788057274897,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 7335,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "311700 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 7335,
            "unit": "ns/op",
            "extra": "311700 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "311700 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "311700 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 69.72,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "34187980 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 69.72,
            "unit": "ns/op",
            "extra": "34187980 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "34187980 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "34187980 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "85018bd9604e7360080dc544c9f53b7709af06dc",
          "message": "feat(observability): a 500 explains itself — message and call-site stack on the trace, the failed statement from the driver, identity on every trace, JSON cause at level error, the failing stage marked, fingerprint groups with a first-occurrence alert (OBSERVABILIDAD-ERRORES-S1)\n\nThe field report's number was 0 of 33: a custom route's 500 reached the\npersisted trace with no message and no stack, its cause went to a\nplain-text log line with no trace_id, and the request line said\nlevel:\"info\". The expensive infrastructure existed; the wires did not.\n\n- writeHandlerError records the cause (or the handler's message) on the\n  trace; ctx.Error(5xx, …) captures runtime.Callers AT THAT CALL, so the\n  first frame is the handler's file:line — never the middleware's; a\n  panic is captured inside the Recoverer's defer on the panicking\n  goroutine (the inlined closure filtered by file); a bare returned error\n  gets the writer's stack with a stack_note saying so (ENG-57).\n- observability.QueryTracer, wired into the pool: the exact statement a\n  query FAILED with is noted on the request's tracker for every route —\n  generated, Ctx or UnsafeTx — template only, never bound values. A 5xx\n  trace shows the driver's message beside the query; client-classified\n  driver errors (400/409/422) leave their cause too.\n- The cause line is JSON at level error with trace_id, request_id,\n  tenant_id, user_id, role, route, status, error, sql, site. The request\n  line is level error for any 5xx. Plain-text sweep: RBAC denials (REST +\n  GraphQL) → structured warn through the context logger, webhook\n  dispatcher, ServeFile, the noop alerter; zerolog.DefaultContextLogger is\n  the engine logger so no context ever swallows a line. Identity on every\n  persisted trace and request line: the JWT middleware writes user/role\n  onto the shared span tracker (the logger runs before it).\n- done is subdivided for custom routes (tx, one query per Ctx call,\n  encode, handler); the failing stage is marked at its source and shown\n  as \"failed here\"; one per request; the 8-slot cap stands.\n- Fingerprint = route + normalized message (uuid/hex/quoted/number →\n  placeholders, SQLSTATE kept) + top frame; error_groups table with\n  count/first/last/users/sample; the panel's Issues tab shows Problems.\n  Against the 105's real traces: 158 5xx → 41 problems. A NEW group alerts\n  on its first trace, braked at 5/tenant/minute with a storm summary.\n- Repro: the panel builds curl from the persisted URL + filtered headers;\n  bodies are OFF by default (A-53), APPXIMO_TRACE_BODY=on keeps 4 KiB\n  redacted. backend-spec §3.9 states what Go can and cannot give.\n- The binary-diff gate gains an error-trace projection probe (an expected,\n  explained DIFF). ABBA on the lab: no_change (median and tail).\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-30T17:17:56Z",
          "tree_id": "f2796eb5a15e9dd1eac0cfb5c19485e1e3b101fb",
          "url": "https://github.com/appximo/appximo/commit/85018bd9604e7360080dc544c9f53b7709af06dc"
        },
        "date": 1788110560786,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6267,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "375828 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6267,
            "unit": "ns/op",
            "extra": "375828 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "375828 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "375828 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 67.51,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35362669 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 67.51,
            "unit": "ns/op",
            "extra": "35362669 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35362669 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35362669 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "4e873b5fc8df40795986d616d01b205111cb1a74",
          "message": "feat(ops): a backup that restores — restore.sh timed and verified against a manifest, one backup SET (dump + uploads + secrets + counts), the installer's nightly timer, off-box copy with encrypted secrets, never-give-up unit, disk + backup liveness alerts in the self-monitor, the recovery promise written with measured numbers (RESILIENCIA-S1)\n\nThe engine had backup.sh and no restore; the installer wrote no timer and did\nnot even install backup.sh unless it sat next to install.sh. Measured on the\ncustomer box in the laboratory (251 248 rows / 124 MB): corrupt database →\nverified back in 13.6 s; lost box → ≈ 4 min + DNS (install.sh 150 s, restore\n12 s); RPO = the timer interval. The 3 a.m. runbook was followed literally\non a wiped box and failed twice (postgres could not read the 0700 backup dir;\na trailing `[ … ] &&` under set -e) — both fixed here. Six failures provoked:\nkill -9 5.6 → 2.96 s (RestartSec=2 + StartLimitIntervalSec=0, which is what\nkeeps a PostgreSQL that fails at boot from stranding the app), reboot 25.8 s,\nslow PostgreSQL waited for, PostgreSQL stopped hot = fast 503s, network DROP =\n503s at 5 s each (ENG-59), disk full unseen until catastrophic (the alert at\n10 % is the guard; an empty status file is an alarm). Layer 5 of the\ncollector reads statfs + last-backup.status with raw syscalls over paths\nprepared once — 1 alloc/tick — and alerts through the existing Alerter.\n\nGates: unit + full lane + make test-all green; lint 0; binary-diff gate\n171 = 171 SAME ×2 on the final binary; ABBA frozen in the lab no_change in\nmedian and tail. ENG-3 → DONE; new OPS-40..43, ENG-58, ENG-59.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-30T18:36:11Z",
          "tree_id": "d739a12213b9fceb031f1695424976dd004a00c6",
          "url": "https://github.com/appximo/appximo/commit/4e873b5fc8df40795986d616d01b205111cb1a74"
        },
        "date": 1788115010246,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6034,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "394442 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6034,
            "unit": "ns/op",
            "extra": "394442 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "394442 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "394442 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.03,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36484375 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.03,
            "unit": "ns/op",
            "extra": "36484375 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36484375 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36484375 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "bc3b735044afe0084c4dee883356367574a361af",
          "message": "feat(resilience,ops): break what we can repair — the breaker sheds a black-holed DB (ENG-59), PostgreSQL auto-restarts after an OOM-kill, checksums default on fresh installs, a fleet audit that says what's missing, ten chaos experiments (CAOS-S1)\n\nENG-59: the breaker's count ledger used gobreaker's default Interval=0 (never\ncleared), so a process warmed by normal traffic could never reach the 60%\ntrip ratio — and a black-holed database keeps in-flight Requests ahead of the\n5-s-lagging TotalFailures so the ratio never crosses it even windowed. Fix:\nInterval=10s + a 20-consecutive-failure rule (an unbroken run = the DB is not\nanswering). Measured on the customer box, warmed process, 30s iptables DROP:\np50 of a failed request 5.00s -> 0.00s, 70% under 200ms; recovery still +0.1s.\nHealthy-path ABBA no_change; binary-diff gate 171/171 SAME.\n\nTwo findings from breaking things, both fixed:\n- PostgreSQL had no auto-restart (Ubuntu ships Restart=no) — an OOM-killed or\n  crashed postmaster left every app on the box down until a human acted (the\n  field OOM incident). install.sh now writes a Restart=on-failure drop-in with\n  RestartPreventExitStatus=SIGINT SIGTERM; provoked -> self-recovers in 5s,\n  intentional stop still stops.\n- RESILIENCIA's layer-5 backup watch never worked on a real install: the 0700\n  root backup dir blocked the unprivileged engine from reading last-backup.status\n  (always \"none\", no alert). Dir is now 0711 (conf bundle stays 0600).\n\nOPS-42: data_checksums default-on for a fresh cluster (enable 0.9s/372MB,\nruntime cost no_change); on a cluster with data, warn + recipe. backup.sh now\nnames the corrupt table in its status and notification, captured in memory so\na full-disk run still reports the cause.\n\nOPS-40: disk + backup cards on /admin Resources (layer 5). fleet-audit.sh:\nper-app \"what is missing\" + box facts (swap, disk, checksums, PG restart).\ninstall.sh preserves operator env keys on a re-run (theme, demo roles, limits).\n\nTen chaos experiments, each with a written hypothesis first (all PASA or\nfixed). PRODUCTION §4.4/§4.5/§4.5b/§4.6 + backend-spec §3.8 updated.\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-30T22:45:15Z",
          "tree_id": "b7624a69de59b0336cb019d983331ff952a01818",
          "url": "https://github.com/appximo/appximo/commit/bc3b735044afe0084c4dee883356367574a361af"
        },
        "date": 1788130018852,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6335,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "376908 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6335,
            "unit": "ns/op",
            "extra": "376908 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "376908 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "376908 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 73.88,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "31965632 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 73.88,
            "unit": "ns/op",
            "extra": "31965632 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "31965632 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "31965632 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "966ca66ebe910440842e0049981c260bc3bb2ed3",
          "message": "docs(backlog): DEPLOY-FLOTA-S1 reviewed — the frozen ABBA verdict (no_change in every cross), the 58's remaining ✗ are Miguel's; deploy-app.sh --help range\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-31T01:54:44Z",
          "tree_id": "37ec9348b53222bf3e861339a182c6672ff33ced",
          "url": "https://github.com/appximo/appximo/commit/966ca66ebe910440842e0049981c260bc3bb2ed3"
        },
        "date": 1788141324205,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6311,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "378170 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6311,
            "unit": "ns/op",
            "extra": "378170 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "378170 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "378170 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.23,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "35476705 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.23,
            "unit": "ns/op",
            "extra": "35476705 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "35476705 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "35476705 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "2a85e0c1da24a0af92558ecc6d1049c86b341f6c",
          "message": "docs: the operator's manual in Spanish (what the engine does, where each thing shows with real screens, every knob with its default and its origin, the recipes with commands and measured times, deploy and upgrading an old box, drill, what it does not do) + the one-page index of the engine kept from rotting by a test; PRODUCTION env table corrected (derived rate limit, APPXIMO_MAX_INFLIGHT, SSE cap); backlog: MANUAL-OPERACION-S1 reviewed, OPS-48/OPS-49/DOC-4 opened (MANUAL-OPERACION-S1)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-31T03:17:20Z",
          "tree_id": "8afa8469b7ca9bce5fb6daef6a860a91f18f54a7",
          "url": "https://github.com/appximo/appximo/commit/2a85e0c1da24a0af92558ecc6d1049c86b341f6c"
        },
        "date": 1788146592644,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6388,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "349862 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6388,
            "unit": "ns/op",
            "extra": "349862 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "349862 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "349862 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 66.77,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "36329575 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 66.77,
            "unit": "ns/op",
            "extra": "36329575 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "36329575 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "36329575 times\n4 procs"
          }
        ]
      },
      {
        "commit": {
          "author": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "committer": {
            "email": "miguel09acosta@gmail.com",
            "name": "Miguel Acosta",
            "username": "miguel09acosta"
          },
          "distinct": true,
          "id": "68e5bb37fa08417d5efef1f5350a511c485f8870",
          "message": "docs: the command center — manual §9 (an app built ON Appximo, on its own box in another region, that shows the whole operation, fills itself from /health, /admin/resources, fleet-audit.sh, the backlog and the handoff, and never leaves the owner without a next step: the three forms, the failure block, «pedir ayuda»); the engine index row; backlog: CENTRO-MANDO-S1 reviewed (the 58's backups verified restorable, the 105's disk 87→69 %, the migration chain rehearsed 58→throwaway, the deleted-corrida near-miss reverted and fixed), OPS-50/51/52 opened (CENTRO-MANDO-S1)\n\nCo-Authored-By: Claude Fable 5 <noreply@anthropic.com>",
          "timestamp": "2026-08-31T04:51:20Z",
          "tree_id": "35467bd8b1d3dd5da3d5ff764705f0e3de0554ae",
          "url": "https://github.com/appximo/appximo/commit/68e5bb37fa08417d5efef1f5350a511c485f8870"
        },
        "date": 1788151914588,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkJWTValidation",
            "value": 6535,
            "unit": "ns/op\t    3072 B/op\t      52 allocs/op",
            "extra": "328461 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - ns/op",
            "value": 6535,
            "unit": "ns/op",
            "extra": "328461 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - B/op",
            "value": 3072,
            "unit": "B/op",
            "extra": "328461 times\n4 procs"
          },
          {
            "name": "BenchmarkJWTValidation - allocs/op",
            "value": 52,
            "unit": "allocs/op",
            "extra": "328461 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck",
            "value": 65.4,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "37163934 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - ns/op",
            "value": 65.4,
            "unit": "ns/op",
            "extra": "37163934 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - B/op",
            "value": 0,
            "unit": "B/op",
            "extra": "37163934 times\n4 procs"
          },
          {
            "name": "BenchmarkRBACCheck - allocs/op",
            "value": 0,
            "unit": "allocs/op",
            "extra": "37163934 times\n4 procs"
          }
        ]
      }
    ]
  }
}