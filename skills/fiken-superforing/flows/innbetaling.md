# Flow: innbetaling, Vipps- and Stripe-oppgjør

Status: to be mapped. Filter with `kunInnbetalinger=true`; `sok=vipps` or `sok=filial`
narrows to settlements.

Known from `docs/browser-workflows.md`: Vipps-oppgjør confirm with `Bekreft dato`, Stripe
payouts (`FILIAL AF BANKING CIRC`) with `OK, gå til neste`, both one-click when the settlement
is booked. Customer payments with an invoice number match the open invoice and confirm with
`OK, gå til neste`. Innbetalinger without a sale are parked.

Fill in the recognition rules, API steps and logging once verified live, following the shape
of [`utbetaling-kjop.md`](utbetaling-kjop.md).
