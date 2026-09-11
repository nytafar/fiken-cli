# Flow: recurring card subscription without monthly receipts

Recognise: the same card text every month (`JUDA* JUDANETWORK USD 4,99`), a first receipt in
the books, and no receipt for later months. Verified 2026-09-11 on 20 months of Vimeo OTT.

1. **Precedent**: `purchases get <slug> --date-ge <a year back> --all --agent`, filter on the
   description and amount. Take `kind`, `account`, `vatType` from the most recent ones; note
   which months have attachments.
2. **Book** each open line as the precedent, NOK from the line, `identifier`
   `<first receipt no>/<yyyy-mm>`, description naming the first receipt. Attach the first
   receipt (download it from the original purchase's `purchaseAttachments[].downloadUrl`).
3. **Internal voucher per month.** Write a spec TSV (`purchaseId`, `bank_date`, `nok_ore`,
   `nok`, `rate`) and hand it to a subagent that renders one A4 PDF per row through headless
   Chromium. The voucher is labelled `INTERNT BILAG`, states supplier, card, bank date, USD
   and NOK amounts, implied rate, account and VAT treatment, and a paragraph saying the
   supplier sends no monthly receipts and the voucher is read together with the first
   receipt attached to purchase `<id>`. It must not imitate the supplier's receipt.
4. **Attach** each voucher to its purchase with `--filename <bank_date>-<name>-internt-bilag.pdf`,
   verify the attachment count went up by one, and log
   `attachment.added --target-type purchase --target-id <id>` per purchase.
5. `confirm --log` the open lines.
