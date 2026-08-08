window.BENCHMARK_DATA = {
  "lastUpdate": 1786160342854,
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
      }
    ]
  }
}