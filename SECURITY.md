# Security policy

## Deployment posture

`dreame-dashboard` is built for a trusted home LAN. By default the web interface has no login password of its own. Do not forward port `9094` on a router or expose the container directly to the public internet.

If you need password protection on the LAN, set `DMD_BASIC_USER` and `DMD_BASIC_PASSWORD_FILE` (pointing to a mode `0600` file mounted read-only under `/secrets`).

## Secrets on disk

- Interactive sign-in saves only the Dreame OAuth session pair in `/data/tokens.json` (mode `0600`). The account password is not written to disk.
- Optional OpenAI API keys saved through `/device` are encrypted at rest with AES-256-GCM using a random local key (`/data/.secret.key`, mode `0600`).
- Passing `DMD_PASSWORD_FILE`, `DMD_BASIC_PASSWORD_FILE`, or `DMD_OPENAI_API_KEY_FILE` reads secrets from mounted files rather than process environment variables.

## Reporting a vulnerability

Report suspected security issues privately through GitHub Security Advisories for this repository instead of opening a public issue. Include reproduction steps and the version reported by `dmd version`.
