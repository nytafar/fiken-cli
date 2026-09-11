# Flow: supplier name on the bank line differs from the suggested purchase

Recognise: `show` text carries the `Antatt` badge and a `Leverandør: <name>` that is not the
bank line's `tittel`. Example 2026-09-07: `tittel` `Mamod Invest AS`, suggestion
`Leverandør: Aas Transport AS`, button `OK, gå til neste`. Fiken matched on amount, date and
payee account; the name is the only disagreement, and a wrong confirm books a payment to the
wrong supplier.

Confirm the clean rows first. Then, for each name-mismatch row:

1. **Evidence**, all read-only:
   - the purchase Fiken proposes: `purchases get --date-le <linje.dato>` filtered on the
     amount, note `supplier.contactId`;
   - the contact: `contacts get <slug> --name "<Leverandør>"`, note `organizationNumber` and
     `bankAccountNumber`; the bank line's `tekst` names the payee account after
     `til kontonummer`, and it should equal the contact's;
   - the register: `curl -s https://data.brreg.no/enhetsregisteret/api/enheter/<orgnr>`,
     field `navn`. A `navn` equal to the bank line's `tittel` means the company was renamed.
2. **Ask the user** with the evidence in one paragraph and three options: confirm and rename
   the contact, confirm only, leave parked. Stop until answered.
3. On confirm: `confirm --log`. On rename: fetch the contact with `contacts get`, set `name`,
   drop `createdDate`, `lastModifiedDate`, `contactPerson`, `notes`, and
   `contacts update <slug> <contactId> --stdin`; log
   `--operation contact.renamed --target-type contact --target-id <id> --source-ref linje:<id>`
   with `--inputs {"from","to","evidence"}`.

Renaming the contact makes the next line from the same payee a clean row.
