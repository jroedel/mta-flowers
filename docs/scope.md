# Flowers for the Blessed Mother — scope

**Feast of Our Lady of Schoenstatt, Shrine at Austin. Saturday 17 October 2026.**
Scoped 2026-09-11, which leaves **36 days**.

A friend built last year's version and it worked — guests reacted well, and the
ask is to have the same thing again. He has just had a baby, so it is rebuilt
from scratch rather than handed over. Nothing of his is being reused, and
nothing of his is being criticised: the shape below is his shape, reconstructed
from the Squarespace setup guide he wrote, a screenshot of the live widget, and
one of its notification emails.

## What it is, from the guest's side

A block on a page of `schoenstatt-austin.us`:

> **Bring flowers for Oct 17?**
> [ Your name ]
> [ 🌷 I'll bring flowers ]
> 🌼 Total commitments: 0

They type a name, press the button, the count goes up. That is the whole
interaction, and its smallness is the feature — last year it drew people in at
a rate a form never would.

## What changes, and what does not

Last year ran on AWS: API Gateway in front of a function, notifications through
Amazon SES, the widget script on CloudFront. This year it is **one Go binary
with a SQLite database on one Vultr VPS**, rented for the month around the
feast. Same behaviour, four fewer vendors.

The guest-visible behaviour is unchanged, and that is deliberate. What is being
replaced is the hosting, not the design.

### In scope

| | |
|---|---|
| Commit to bringing flowers | name → stored → notification sent |
| Cancel a commitment | last year sent a "Flower Commitment Canceled" email with a running count, so it existed |
| Live total count | the line under the button; it is what pulls people toward the goal |
| One signup per browser | cookie, as last year. Politeness, not enforcement — trivially bypassed and that is fine |
| Notification email per commitment | to a few named people, on both commit and cancel |
| Admin sign-in by magic link | no password to set, lose or share; see below |
| Admin: change the date, reset the count, remove duplicates | the three things that actually go wrong during an event |

### Out of scope

**Group registration.** The "coming with 10 or more" form was a native
Squarespace form last year, separate from the widget and not part of it.
It stays native. Rebuilding it in Go would be new work dressed as parity.

## Architecture

```
schoenstatt-austin.us  (Squarespace)
   page header injection:  window.FLOWERS_API, window.FLOWERS_EVENT_ID
   page content:           <div id="flowers-widget"> + <script src=…/flowers.js>
        │  HTTPS, CORS allowlisted to the Squarespace origin
        ▼
flowers.schoenstatt.link   →   Vultr VPS
   one Go binary: net/http, modernc.org/sqlite (cgo-free), embedded flowers.js
   commitments.db on local disk
        │  SMTP AUTH, submission
        ▼
mail.your-server.de:587  →  dedi2934.your-server.de  →  recipients
```

The Go binary serves `flowers.js` itself, so CloudFront has no replacement and
needs none — one origin for the script and the API is one certificate and one
CORS policy instead of two.

### Why the mail leaves through Hetzner

Sending straight from the Vultr box would be less code and would not work.
`schoenstatt.link` publishes `v=spf1 mx ~all` and now enforces
`p=quarantine` — a Vultr IP is covered by neither SPF nor DKIM, so
notifications would be quarantined or silently dropped, which for this system
means nobody learns that anybody signed up.

Submitting to Hetzner's endpoint with SMTP AUTH avoids that, and
`EMAIL-INFRASTRUCTURE.md` establishes why by tracing the hops: submission
relays to our own `dedi2934`, which signs with DKIM and delivers from
`78.47.5.22` — the address SPF authorises. Mail from the Vultr box therefore
arrives with the same authentication as mail sent from the server itself.
**This is worth re-verifying with one real message before the widget goes
live**, because that document dates from 2026-08-14 and it says to re-verify.

`From:` is an address on `schoenstatt.link`, not on `schoenstatt-austin.us` —
that domain is on Squarespace and Network Solutions DNS, has no DKIM of ours,
and giving it one is a bigger project than this one.

### Why that hostname

`flowers.schoenstatt.link`. DNS for `schoenstatt.link` is on Hetzner
nameservers we administer, so the record is ours to add today.
`schoenstatt-austin.us` lives at Network Solutions (`ns57/ns58.worldnic.com`)
and would mean waiting on whoever holds that login — a dependency worth
avoiding when the deadline is fixed by a feast day.

Note this is a *web* subdomain only. `EMAIL-INFRASTRUCTURE.md` records that
subdomains of `schoenstatt.link` do **not** get mail for free — Exim rejects
them — but nothing here needs to receive mail at that name.

## The Squarespace side

The page injection changes only in its values:

```html
<script>
  window.FLOWERS_API = "https://flowers.schoenstatt.link/";
  window.FLOWERS_EVENT_ID = "church-flowers-2026-10-17";
</script>
```

Keeping last year's `FLOWERS_*` names and `#flowers-widget` id means the setup
guide he wrote still describes the procedure, and the only edit is two strings.
A replacement guide comes with the handover.

**This needs somebody with Squarespace edit access**, and it is the one step
that cannot be done from here.

## The admin side

Three jobs, named because they are the three that go wrong during an event:
**change the date**, **reset the count**, **delete duplicates**. Two more come
along for the ride: **see the list** — you cannot remove a duplicate you cannot
see — and **download it as CSV**, which is how the list outlives the instance.

All of it is pages in the browser. Nothing here needs a terminal; the only
command-line work on this project is deploying and provisioning.

### Sign-in is a magic link

An address is typed at `/admin`; if it is on a short allowlist the server mails
a one-time link, and following it sets a session cookie. No password is created,
stored, rotated, or shared between the people who need this. For a tool used by
a handful of people for five weeks, a password is a thing to leak and a magic
link is not.

Mechanics: 32 random bytes, stored hashed, single-use, valid fifteen minutes.
The session cookie is `HttpOnly`, `Secure`, `SameSite=Lax`, and expires after
the event. The allowlist lives in `ADMIN_EMAILS` in the kit — an address not on
it never receives a link, and the page says the same "check your email" either
way.

### If mail is down, so is sign-in

A magic link arrives by email, so a mail outage locks out the one person who
would fix it. The way out stays in the browser: `/admin` also accepts a
fallback password, set once as `ADMIN_FALLBACK_PASSWORD`. Normal use is the
link; the password is for the bad morning.

### Reset and delete are undoable

"Reset the count" and "delete duplicates" both remove commitments people made,
and both will be done in a hurry on a phone. They set a `deleted_at` column
instead of deleting the row, so the admin list can show them and put them back.
One column, and a misfired reset on the morning of the feast stops being
permanent. This is an undo button, not a backup.

## CI/CD

Every push to `main` deploys, as in eumaeus, and the shape is taken from
`eumaeus/.github/workflows/deploy.yml` because it is already reasoned through:

| | |
|---|---|
| `ci.yml` | tests, lint, and a check that the binary is actually static. Runs on pushes and pull requests |
| `deploy.yml` | on push to `main`, ignoring `docs/**` and `**/*.md`. Build `linux/amd64` → bundle binary + systemd unit + Caddyfile → `scp` → run the installer → health check → **roll back if it does not answer** |
| concurrency | one deploy at a time. Two runs racing to restart the same service is not a state worth reasoning about |
| host key | pinned via `DEPLOY_KNOWN_HOSTS`. No `StrictHostKeyChecking=no` anywhere — accepting an unknown host key means offering the deploy key to whoever answered |
| actions | pinned by commit SHA, not by tag. A tag is a pointer somebody else can move |

The health check is what makes "every push deploys" safe to say: the workflow
is not finished when the file lands, it is finished when the new binary answers
`/healthz` through Caddy over the real hostname. If it does not, the previous
binary goes back automatically. A rollback that is a suggestion in a log is not
a rollback.

### One thing deliberately dropped

eumaeus signs its deploy bundle with cosign, keylessly, and its installer
verifies the signature before unpacking a byte. That defends against a leaked
deploy key being used to run attacker-chosen code, and it is genuinely good.

**It is not being copied here**, and the trade should be explicit rather than
quiet. The installer-side half is the expensive part — eumaeus's is 409 lines
— and what it protects differs by an order of magnitude: eumaeus holds a
financial ledger and the restic passwords for a fleet of laptops; this holds a
list of names of people bringing flowers, for five weeks, on an instance that
is then destroyed. The residual risk is stated plainly: **whoever holds the
deploy key can run code on this box.** The key's only home is one GitHub
secret, it reaches nothing else, and the box has nothing else on it.

If you would rather have parity, say so — the pieces exist next door and it is
perhaps half a day.

### Provisioning

eumaeus already drives Vultr with OpenTofu (`deploy/tofu`, provider
`vultr/vultr` 2.32.0) and has the cloud-init, Caddyfile and systemd units to
match. Those are the model for this instance, reduced to one server with no
firewall automation — for the reason eumaeus's own deploy workflow gives at
length: a Vultr key is account-wide and full-access, so it never goes near CI.
It stays on this machine, in `vultr-provisioning.env`.

## Credentials

One file, `~/.config/mta-flowers.env`, 0600, copied to your password manager.
The app reads the same names from `/etc/mta-flowers.env` on the server; the
deploy values become GitHub Actions secrets. The Vultr API key is the one
exception — it stays on your machine and never reaches the server or CI,
because it can delete the instance and nothing on the instance needs it.

That is the whole policy. This holds a list of names for five weeks.

## Risks

**The deadline does not move.** 17 October is a feast day. Cutting scope is
possible; cutting the date is not. Everything above is sized to be finished
with slack, and the slack is the point.

**One VPS, no redundancy, no backups.** Correct for a month-long event: if the
box is down, the button does nothing until it is up. The data is a list of
names that people would happily give again.

**Mail is now load-bearing twice.** It carries the notifications *and* the
admin sign-in, so an outage at Hetzner takes both. The fallback password is the
answer for the second; for the first, a commitment is still recorded even when the
notification fails, and the admin list is the record of truth. The button must
never fail because mail failed — sending happens after the row is committed,
and a send error is logged, not returned to the guest.

**The rented month must outlive the event.** The list is wanted after the feast,
to know who came. Download it from the admin page before the instance is
destroyed — that is the only copy.

## Open questions

1. **Who receives the notifications?** "A few people" — the addresses, into
   `NOTIFY_RECIPIENTS` in the kit. Also whether each wants every commitment or
   a daily digest: at 100 commitments, per-commitment is 100 emails each.
2. **Which addresses may sign in as admin?** Into `ADMIN_EMAILS`. Your own
   (`frjeff@schoenstatt.us`) is assumed to be one of them.
3. **Is the goal still 100 people?** Last year's copy asked for 100.
4. **Arrival time and instructions** — last year: arrive by 10am, leave the
   flowers behind the communion rail in the Shrine. Confirm for this year.
5. **Who has Squarespace edit access**, and are they available in the week
   before the feast?
6. **Is any of last year's data worth carrying over** — the list of who
   committed in 2025 — or does this start empty?

## Related

- `/opt/projects/EMAIL-INFRASTRUCTURE.md` — the mail facts this depends on
