# Flow: innbetaling, Vipps- and Stripe-oppgjør

Recognise: positive `linjeBelop`. Filter with `--kun-innbetalinger true`. Read the suggestion
text with `show`; the wording decides the branch. Verified live 2026-09-11 on `nyta`.

| Suggestion text | Meaning | Action |
|---|---|---|
| `Intern overføring av oppgjør po_… til bankkonto`, `Regnskapskonto: 1960:10001`, button `OK, gå til neste` | Stripe payout (`tittel` = `Overføring: FILIAL AF BANKING CIRC`) already booked from the Stripe account | `confirm` |
| `Denne linjen kan knyttes til et allerede bokført betalingsoppgjør`, button `Bekreft dato` | Vipps-oppgjør (`tittel` = `Overføring av Vipps-oppgjør <nr>`) already booked | `confirm --label "Bekreft dato"` |
| `Betaling for salg nr <N>`, button `Registrer ny betaling` | Customer paid invoice N, payment not yet registered | register the payment, then `confirm` (below) |
| `Dette er allerede lagt inn i Fiken`, button `OK, gå til neste` | Any other booked posting (lønn, forskuddstrekk, internal transfer) | `confirm` |
| `Registrer nytt salg` or `Alternativer` only | No sale behind the money | park, reason `mangler salg` |

## Customer invoice payment

1. Sale id: `fiken-cli sales get <slug> --date-ge <2 months back> --date-le <linje.dato> --agent`,
   pick `saleNumber == N`, check `outstandingBalance` equals `linjeBelop` in øre.
2. `fiken-cli sales payments create-sale <slug> <saleId> --date <linje.dato> --account 1920:10001 --amount <linjeBelop øre> --agent`
3. `log-event --operation payment.registered --surface api --target-type sale --target-id <saleId> --source-ref linje:<id>`
4. Rerun `show`: the text becomes `Betaling registrert via API` with `OK, gå til neste`. `confirm --log`.

A Stripe payout or Vipps-oppgjør without the booked-settlement wording means the settlement
import is behind; park it with the text shown rather than registering anything by hand.
