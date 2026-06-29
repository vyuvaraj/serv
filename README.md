# ServMail

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
