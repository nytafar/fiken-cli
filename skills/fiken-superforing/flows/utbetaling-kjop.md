# Flow: utbetaling with supplier receipt

Recognise: negative `linjeBelop`, a supplier name in `tittel`, and a receipt for it in the
inbox (`fiken-cli inbox get <slug> --status unused --agent`). Match on supplier and amount;
for card lines in currency the receipt amount is `besteBelop`. Card subscriptions bill the
**previous** period, so the receipt is often dated up to a month before the line: a 4 May
charge pays the 30 April invoice.

Without a receipt, park the line with reason `mangler bilag`.

1. **Read the receipt.** Download it (`documentUrl` with the `access_token` from
   `~/.config/fiken-cli/config.toml` as a Bearer token, CLI download is nytafar/fiken-cli#23) into
   `bilag/<documentId>.pdf`, then `pdftotext -layout`. Take the invoice number and the tax
   block from the PDF, not from history.
2. **Account and VAT.** `fiken-cli vendor-profile <contactId> --company <slug> --agent` gives
   the modal account. The tax block on the receipt decides `vatType`: 0 % with a reverse
   charge note is `HIGH_FOREIGN_SERVICE_DEDUCTIBLE` with `vat: 0`; 25 % Norwegian MVA on the
   invoice (AWS does this) is `HIGH` with `netPrice` and `vat` split as printed.
3. **Create the purchase**, NOK, paid, from the line:
   ```json
   {"date": "<linje.dato>", "kind": "supplier", "currency": "NOK", "supplierId": <id>,
    "identifier": "<invoice no>", "paid": true, "paymentDate": "<linje.dato>",
    "paymentAccount": "1920:10001",
    "lines": [{"description": "...", "account": "6553", "vatType": "HIGH_FOREIGN_SERVICE_DEDUCTIBLE",
               "netPrice": <abs(linjeBelop) in øre>, "vat": 0}]}
   ```
   `fiken-cli purchases create <slug> --stdin --agent`, then look the id up with
   `purchases get --date-ge/--date-le --no-cache` (create returns no id).
   If the purchase already exists unpaid, register the payment instead:
   `fiken-cli purchases payments create-purchase <slug> <purchaseId> --date <linje.dato> --account 1920:10001 --amount <øre>`
   (foreign-currency purchase: add `--currency <ccy> --amount-in-nok <linjeBelop øre>`;
   Fiken posts the rate difference to 8160 itself).
4. **Attach the receipt**, then verify before touching the inbox:
   `fiken-cli purchases attachments add-to-purchase <slug> <purchaseId> --file <pdf> --filename <name>.pdf --attach-to-sale`
   (`--attach-to-sale` is the flag for a supplier purchase; without `--filename` Fiken answers
   HTTP 400 `filename must be specified`). Done when `purchaseAttachments` has length 1.
5. **Delete the inbox document**: `fiken-cli inbox delete-document <slug> <documentId> --agent`.
   The attachment survives. An undeleted inbox document keeps the panel suggesting
   `Registrer nytt kjøp`.
6. **Reload the panel**, then `confirm(linje.id)`. Expect `OK, gå til neste`. Anything else:
   park with the buttons present.
7. **Log**: `log-event --operation purchase.created --surface api --target-id <purchaseId> --source-ref inbox:<docId>`,
   `--operation inbox.deleted --source-ref inbox:<docId>`, and
   `--operation match.confirmed --surface browser --source-ref linje:<id>`, all with
   `--company <slug>` and one `--correlation-id` per run.

Done for the line when it is gone from the list, the purchase has its attachment, and all
three events are logged.
