# oc-cron Contract

`oc-cron` is the version adapter between oc-manager and the Hermes Cron implementation bundled in each runtime variant.

## Versioning

- Contract version is `1.0`.
- Manager may consume any `1.x` adapter.
- Breaking changes require `2.0`; manager must reject unsupported major versions.
- Each runtime variant owns its adapter implementation and hides upstream Hermes CLI/API/file differences.

## Envelope

Successful non-streaming commands write exactly one JSON object to stdout:

```json
{"ok":true,"data":{}}
```

Failed commands write:

```json
{"ok":false,"error":{"code":"BAD_REQUEST","message":"schedule is required"}}
```

## Error Codes

| Code | Meaning |
|---|---|
| `BAD_REQUEST` | Invalid user input or unsafe path |
| `NOT_FOUND` | Job or output file not found |
| `UNSUPPORTED` | Runtime does not include real Hermes Cron |
| `HERMES_CLI_FAILED` | `hermes cron` failed |
| `INTERNAL` | Adapter could not parse or normalize runtime data |

## Verbs

- `capabilities`
- `status`
- `list --all`
- `show --id <job_id>`
- `create`
- `edit --id <job_id>`
- `pause --id <job_id>`
- `resume --id <job_id>`
- `run --id <job_id>`
- `remove --id <job_id>`
- `history --id <job_id>`
- `output --id <job_id> --file <file_name>`
