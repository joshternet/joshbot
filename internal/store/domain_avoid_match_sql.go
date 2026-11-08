package store

// domainAvoidHostnameMatchSQL is the predicate used when an unnested avoid
// pattern should exclude parsed.hostname. Callers must expose excluded.pattern
// and parsed.hostname in scope.
//
// Only domain, family, and glob forms are evaluated here. Pass patterns through
// domainavoid.NonRegexPatterns first. Explicit re: RE2 rules are applied in Go
// via domainavoid.Matches so SQL and Go never disagree on regex engines.
const domainAvoidHostnameMatchSQL = `
(
	right(excluded.pattern, 2) = '.*'
	AND position(
		'*' in left(
			excluded.pattern,
			greatest(length(excluded.pattern) - 2, 0)
		)
	) = 0
	AND (
		parsed.hostname LIKE
			replace(
				replace(
					replace(
						left(
							excluded.pattern,
							length(excluded.pattern) - 2
						),
						E'\\',
						E'\\\\'
					),
					E'%',
					E'\\%'
				),
				E'_',
				E'\\_'
			) || '.%'
		ESCAPE E'\\'
		OR parsed.hostname LIKE
			'%.' || replace(
				replace(
					replace(
						left(
							excluded.pattern,
							length(excluded.pattern) - 2
						),
						E'\\',
						E'\\\\'
					),
					E'%',
					E'\\%'
				),
				E'_',
				E'\\_'
			) || '.%'
		ESCAPE E'\\'
	)
)
OR (
	position('*' in excluded.pattern) > 0
	AND NOT (
		right(excluded.pattern, 2) = '.*'
		AND position(
			'*' in left(
				excluded.pattern,
				greatest(length(excluded.pattern) - 2, 0)
			)
		) = 0
	)
	AND parsed.hostname LIKE replace(
		replace(
			replace(
				replace(
					excluded.pattern,
					E'\\',
					E'\\\\'
				),
				E'%',
				E'\\%'
			),
			E'_',
			E'\\_'
		),
		'*',
		'%'
	) ESCAPE E'\\'
)
OR (
	position('*' in excluded.pattern) = 0
	AND (
		parsed.hostname = excluded.pattern
		OR parsed.hostname LIKE
			'%.' || replace(
				replace(
					replace(
						excluded.pattern,
						E'\\',
						E'\\\\'
					),
					E'%',
					E'\\%'
				),
				E'_',
				E'\\_'
			)
		ESCAPE E'\\'
	)
)
`
