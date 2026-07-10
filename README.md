# ServMail

```bash
docker run -p 8088:8088 ghcr.io/vyuvaraj/servmail:latest
```

ServMail is a transactional notification engine of the Servverse ecosystem.

## Features
- **Multi-channel delivery**: Sends notifications via SMTP email, Slack webhook alerts, and SMS text messages.
- **Go template rendering**: Compiles and executes Go templates dynamically with user-provided JSON context payloads.

## API Endpoints
- `POST /api/mail/send` - Send a notification with parameters:
  - `channel` - e.g. `"email"`, `"slack"`, `"sms"`
  - `target` - e.g. email address, phone number, webhook URL
  - `template` - template text
  - `context` - template context variables

## Getting Started
To run the integration tests locally:
```bash
go test -v ./...
```

---

## Use Without Servverse (Standalone Quickstart)

`ServMail` can run as an independent transactional notification sender:
1. Start `ServMail` with `--standalone` to use local template directories rather than S3 storage:
   ```bash
   ./servmail --port 8088 --standalone --template-dir ./templates
   ```
2. Trigger notification dispatches:
   ```bash
   curl -X POST http://localhost:8088/api/mail/send \
     -H "Content-Type: application/json" \
     -d '{
       "channel": "email",
       "target": "receiver@example.com",
       "template": "Hello {{.Name}}!",
       "context": {"Name": "Bob"}
     }'
   ```

