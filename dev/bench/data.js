window.BENCHMARK_DATA = {
  "lastUpdate": 1785950971390,
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
      }
    ]
  }
}