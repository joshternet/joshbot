# Third-party licenses

JoshBot itself is licensed under the BSD 3-Clause License in [LICENSE](LICENSE).

This file lists direct Go module dependencies declared in `go.mod` and the
licenses that apply to them. Indirect dependencies are pulled in by those
modules; see each module’s repository for the full transitive set.

| Module | License | Copyright / notices |
| --- | --- | --- |
| `github.com/jackc/pgx/v5` | MIT | Copyright (c) 2013-2024 Jack Christensen |
| `github.com/jackc/pgpassfile` | MIT | Copyright (c) 2019 Jack Christensen |
| `github.com/jackc/pgservicefile` | MIT | Copyright (c) 2020 Jack Christensen |
| `github.com/jackc/puddle/v2` | MIT | Copyright (c) 2018 Jack Christensen |
| `golang.org/x/net` | BSD-3-Clause | Copyright (c) 2009 The Go Authors |
| `golang.org/x/sync` | BSD-3-Clause | Copyright (c) 2009 The Go Authors |
| `golang.org/x/text` | BSD-3-Clause | Copyright (c) 2009 The Go Authors |

Test-only or tooling modules that may appear in the module graph (for example
`github.com/stretchr/testify` and related packages) are not required at
runtime. Consult their upstream repositories for license text when redistributing
build or test tooling.
