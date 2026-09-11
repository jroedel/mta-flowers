# Adding the widget to the Squarespace page

For whoever has edit access to `schoenstatt-austin.us`. About ten minutes.

This is the same procedure as last year's guide, because the widget kept last
year's names on purpose — `FLOWERS_API`, `FLOWERS_EVENT_ID` and the
`flowers-widget` div. **The only thing that changes is two strings**, so if you
still have the old code block, editing it in place also works.

## Step 1: the page header

1. Open the page where the widget should appear, and click **Edit**.
2. In the left sidebar: **Settings** → **Advanced** → **Page Header Code Injection**.
3. Paste this exactly:

```html
<script>
  window.FLOWERS_API = "https://flowers.schoenstatt.link/";
  window.FLOWERS_EVENT_ID = "church-flowers-2026-10-17";
</script>
```

4. **Save**.

## Step 2: the widget itself

1. Still editing the page, click **+** where the widget should go.
2. Choose **Code** (it may be under **More**).
3. Paste this exactly:

```html
<div id="flowers-widget">
  <noscript>Please enable JavaScript to sign up.</noscript>
</div>
<script src="https://flowers.schoenstatt.link/flowers.js" defer></script>
```

4. **Apply**, then **Save**.

## Step 3: check it

1. **Preview** the page. The green card should appear with a name box and the
   "I'll bring flowers" button.
2. Sign up with a test name. The count should go up immediately.
3. Remove your test entry from the admin page — see below — so the count starts
   the event at zero.
4. **Publish**.

## What changed from last year, and why

Last year the script came from CloudFront and the API from AWS API Gateway.
Both are now the same server, `flowers.schoenstatt.link`, which is why there is
one hostname in this guide instead of two.

**One real behavioural change.** Last year's widget remembered you with a
cookie. A cookie set by the API's domain inside a page on
`schoenstatt-austin.us` is a *third-party* cookie — Safari blocks those by
default and Chrome restricts them, so "you already signed up" worked for some
guests and not others with no pattern anybody could explain. The widget now
keeps that marker in `localStorage` on the Squarespace page itself, which is
first-party and has none of those problems.

Nobody will notice the difference except that it now works everywhere.

## The admin page

`https://flowers.schoenstatt.link/admin`

Type your address and a sign-in link arrives by email; it works once and lasts
fifteen minutes. From there you can change the date, edit what guests are told,
remove duplicates, download the list as a spreadsheet, and set the count back
to zero. Nothing is ever really deleted — anything removed can be put back.

If email is not working, the same page takes a shared password instead. That is
the way in on the day mail is down, which is the day you will need it.

## If something is wrong

**The widget does not appear.** Check both snippets saved — the page header
*and* the page content are two separate saves, and missing the first is the
usual cause. Then reload with a hard refresh.

**It appears but says it cannot reach the sign-up.** The server is down or the
hostname is wrong. Open `https://flowers.schoenstatt.link/healthz` directly: it
should say `ok`.

**Nobody is getting the notification emails.** The commitments are still being
recorded — the button and the database do not depend on mail. Check the admin
page for the real list; it is the record of truth.
