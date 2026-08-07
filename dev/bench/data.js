window.BENCHMARK_DATA = {
  "lastUpdate": 1786137264350,
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
      }
    ]
  }
}