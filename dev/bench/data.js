window.BENCHMARK_DATA = {
  "lastUpdate": 1785967276440,
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
      }
    ]
  }
}