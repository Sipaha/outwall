import type { AuditBody } from '../lib/types'

/** Pretty-prints a captured request/response body: formats JSON when possible, falls back to
 *  raw text, and shows metadata for binary/absent bodies. */
export function JsonView({ body }: { body: AuditBody }) {
  if (body.body == null) {
    return (
      <div className="text-muted-foreground text-xs font-mono">
        [{body.content_type || 'binary'}] {body.size} bytes · sha256 {body.sha256.slice(0, 12)}…
        {body.truncated && ' · truncated'}
      </div>
    )
  }
  let text = body.body
  if ((body.content_type || '').includes('json')) {
    try {
      text = JSON.stringify(JSON.parse(body.body), null, 2)
    } catch {
      /* keep raw */
    }
  }
  return (
    <pre className="text-xs font-mono overflow-auto max-h-80 bg-background rounded p-2 border border-border">
      {text}
      {body.truncated && '\n… [truncated]'}
    </pre>
  )
}
