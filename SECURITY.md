# Security Policy

## Supported versions

| Version | Supported |
|---------|-----------|
| `main` / latest release | Yes |
| Older tags | Best-effort |

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Report privately via one of:

- GitHub **Security Advisories** for this repository (preferred): *Security → Report a vulnerability*
- Email maintainers at: **security@nudge.works** (replace with the address your org uses)

Include:

- Affected version / commit
- Description and impact
- Reproduction steps or PoC (if available)
- Any suggested fix

We aim to acknowledge reports within **7 days** and coordinate a fix and disclosure timeline.

## Security notes for operators

- Treat MySQL DSNs as secrets (use env expansion `${PRIMARY_DSN}`)
- Prefer least-privilege DB users:
  - Primary: `SELECT`, `DELETE` on archived tables
  - Housekeeping: `SELECT`, `INSERT`, `CREATE`, `UPDATE` (for state tables / schema ensure)
- Do not commit production credentials
- Review `filter` expressions carefully (they are interpolated into SQL `AND (...)` clauses)
