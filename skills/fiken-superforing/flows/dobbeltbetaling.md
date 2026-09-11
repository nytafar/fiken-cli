# Flow: one invoice paid twice, supplier applied the extra to the next invoice

Recognise: two lines, same supplier, same amount, and `show` says `Samme beløp finnes flere
steder` with radio candidates instead of `OK, gå til neste`. Typically a card auto-charge
plus a manual giro (the giro `tekst` carries a KID built on the invoice number). The next
invoice from the supplier shows `Paid` equal to its total and `To Pay 0.00`.

Verified live 2026-09-11, Servetheworld 11.05.2026, two lines of -173,75.

1. **First line** is the payment already booked on the original purchase. Its candidate is
   that purchase's payment transaction.
2. **Second line** is a prepayment. Book the next invoice as a normal paid purchase
   ([`utbetaling-kjop.md`](utbetaling-kjop.md)) with `date` = invoice date and
   `paymentDate` = the second line's `dato`. Fiken accepts a payment date before the invoice
   date; the supplier account carries the credit in between, so no stash account is needed.
3. Rerun `show` on both lines. Each now lists two radio candidates; the `value` is the
   transaction id of a payment. `purchases get` gives `transactionId` per purchase to tell
   them apart.
4. `confirm <line> --pick <transactionId> --log` on the first line, original payment on the
   giro line. The button is `Gå til neste`; with a radio selected it links and removes the row.
5. The other line then has one candidate left and renders no radio: the text reads
   `Er dette riktig forslag?: Betaling (faktura #…)` and the primary button is still
   `Gå til neste`. Read the invoice number in `show`, then
   `confirm <line> --label "Gå til neste" --log`.

Two lines gone and two `match.confirmed` events with `candidate` in `inputs` means done.
