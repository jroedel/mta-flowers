# Adding the widget to the Squarespace page

For whoever has edit access to `schoenstatt-austin.us`. About ten minutes.

**One snippet, in one place.** Last year's guide had two — a header block
setting `window.FLOWERS_API`, and the widget itself. The header block is gone:
the script now works out where its own API is from the URL it was loaded from.
That was not a tidy-up. The two-snippet version was tried on the live site
first, the header half did not take, and the page showed "the flowers widget
is not configured yet" with nothing to say why. A step that can half-succeed
silently is a step worth deleting.

If you already pasted the old header block, you can leave it — it is still
honoured — but you can also delete it and nothing changes.

## Add the widget

1. Open the page where the widget should appear and click **Edit**.
2. Click **+** where you want it, and choose **Code** (it may be under **More**).
3. Paste this exactly:

```html
<div id="flowers-widget">
  <noscript>Please enable JavaScript to sign up.</noscript>
</div>
<script src="https://flowers.schoenstatt.link/flowers.js" defer></script>
```

4. **Apply**, then **Save**.

That is the whole installation. The widget draws its own card, name box, button
and count, and it reads the date and the instructions from the server — so
changing them on the admin page changes them on the site with no edit here.

Keep your own **FLOWERS FOR THE BLESSED MOTHER** heading and the paragraph
under it as ordinary Squarespace text. The widget is only the green card.

## Check it

1. **Preview** the page. The green card should appear with a name box and the
   "I'll bring flowers" button.
2. Sign up with a test name. The count should go up immediately.
3. Remove your test entry from the admin page — see below — so the count starts
   the event at zero.
4. **Publish**.

## What changed from last year, and why

Last year the script came from CloudFront and the API from AWS API Gateway.
Both are now the same server, `flowers.schoenstatt.link` — which is also why
the widget can find itself and the header snippet is gone.

**One real behavioural change for guests.** Last year's widget remembered you with a
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
