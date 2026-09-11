# Flow: innbetaling, Vipps- and Stripe-oppgjør

Recognise: positive `linjeBelop`. Filter with `--kun-innbetalinger true`. Read the suggestion
text with `show`; the wording decides the branch. Verified live 2026-09-11 on `nyta`.

| Suggestion text | Meaning | Action |
|---|---|---|
| `Intern overføring av oppgjør po_… til bankkonto`, `Regnskapskonto: 1960:10001`, button `OK, gå til neste` | Stripe payout (`tittel` = `Overføring: FILIAL AF BANKING CIRC`) already booked from the Stripe account | `confirm` |
| `Denne linjen kan knyttes til et allerede bokført betalingsoppgjør`, button `Bekreft dato` | Vipps-oppgjør (`tittel` = `Overføring av Vipps-oppgjør <nr>`) already booked | `confirm --label "Bekreft dato"` |
| `Betaling for salg nr <N>`, button `Registrer ny betaling` | Customer paid invoice N, payment not yet registered | register the payment, then `confirm` (below) |
| `Dette er allerede lagt inn i Fiken`, button `OK, gå til neste` | Any other booked posting (lønn, forskuddstrekk, internal transfer) | `confirm` |
| `Registrer nytt salg` or `Alternativer` only, `tittel` names a supplier already in the books | Supplier refund | book a credit note (below), then `confirm` |
| `Registrer nytt salg` or `Alternativer` only | No sale behind the money | park, reason `mangler salg` |

## Customer invoice payment

1. Sale id: `fiken-cli sales get <slug> --date-ge <2 months back> --date-le <linje.dato> --agent`,
   pick `saleNumber == N`, check `outstandingBalance` equals `linjeBelop` in øre.
2. `fiken-cli sales payments create-sale <slug> <saleId> --date <linje.dato> --account 1920:10001 --amount <linjeBelop øre> --agent`
3. `log-event --operation payment.registered --surface api --target-type sale --target-id <saleId> --source-ref linje:<id>`
4. Rerun `show`: the text becomes `Betaling registrert via API` with `OK, gå til neste`. `confirm --log`.

## Supplier refund (credit note)

Verified 2026-09-11: Agentic Engineer refunded USD 199, bank line +1 798,94 on 27.05.2026.

1. Find the original purchase (`purchases get`, match supplier or description and the
   currency amount); take its `account` and `vatType`.
2. Get the refund receipt as a PDF into `bilag/`. An email transcription rendered to PDF is
   acceptable when the supplier sent no document; add a `Bokføringsnotat` box naming the bank
   date, NOK amount and the original purchase id.
3. Create a negative cash purchase from the line, no contact:
   ```json
   {"date": "<linje.dato>", "kind": "cash_purchase", "currency": "NOK", "identifier": "<receipt no>",
    "paid": true, "paymentDate": "<linje.dato>", "paymentAccount": "1920:10001",
    "lines": [{"description": "Kreditnota: refusjon …, jf. kjøp <purchaseId>",
               "account": "<original>", "vatType": "<original>", "netPrice": -<linjeBelop øre>, "vat": 0}]}
   ```
   Fiken accepts the negative amount and books the payment as money in on 1920.
4. Attach the PDF, log `purchase.created` with `inputs.credit_note: true` and
   `inputs.reverses_purchase`, rerun `show`: the text reads `Kontantkjøp registrert via API`
   with `OK, gå til neste`. `confirm --log`.

## Not yet verified

Park these with the text shown; the fix path is unknown, so no one should improvise one:
- a Stripe payout or Vipps-oppgjør without the booked-settlement wording (settlement import
  behind?);
- a customer payment that differs from `outstandingBalance` (partial payment, bank fee).
