# Flow: utbetaling with supplier receipt

Recognise: negative `linjeBelop`, a supplier name in `tittel`, and a receipt or invoice for it
in the inbox (`fiken-cli inbox get <slug> --status unused --agent`, match on supplier and
amount; for card lines in currency the inbox amount is the `besteBelop` currency amount).

If no receipt exists, park the line with reason `mangler bilag`. Do not book.

1. **Account and VAT** from history: `fiken-cli vendor-profile <contactId> --company <slug> --agent`.
   Foreign services are typically `6553` + `HIGH_FOREIGN_SERVICE_DEDUCTIBLE`.
2. **Create the purchase**, NOK, paid, from the line:
   ```json
   {"date": "<linje.dato>", "kind": "supplier", "currency": "NOK", "supplierId": <id>,
    "identifier": "<invoice no>", "paid": true, "paymentDate": "<linje.dato>",
    "paymentAccount": "1920:10001",
    "lines": [{"description": "...", "account": "6553", "vatType": "HIGH_FOREIGN_SERVICE_DEDUCTIBLE",
               "netPrice": <abs(linjeBelop) in øre>, "vat": 0}]}
   ```
   `fiken-cli purchases create <slug> --stdin --agent`, then look the id up with
   `purchases get --date-ge/--date-le` (create returns no id).
   If the purchase already exists unpaid, register the payment instead:
   `fiken-cli purchases payments create-purchase <slug> <purchaseId> --date <linje.dato> --account 1920:10001 --amount <øre>` (for a foreign-currency purchase add `--currency <ccy> --amount-in-nok <linjeBelop øre>`; Fiken posts the rate difference to 8160 itself).
3. **Attach the receipt**: `fiken-cli purchases attachments add-to-purchase <slug> <purchaseId> --file <pdf> --attach-to-sale`
   (`--attach-to-sale` is the flag for a supplier purchase).
4. **Delete the inbox document**: `fiken-cli inbox delete-document <slug> <documentId> --agent`.
   The attachment is a separate file and survives. An undeleted inbox document keeps the
   panel suggesting `Registrer nytt kjøp`.
5. **Reload the panel**, then `confirm(linje.id)`. Expect `OK, gå til neste`. Anything else:
   park with the buttons present.
6. **Log**: `log-event --operation purchase.created --surface api --source-ref purchase:<id>`,
   `--operation inbox.deleted --source-ref inbox:<docId>`, and
   `--operation match.confirmed --surface browser --source-ref linje:<id>`, all with `--company <slug>`.

Done for the line when it is gone from the list and all three events are logged.
