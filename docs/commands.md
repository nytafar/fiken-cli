# Command reference

Generated read/write commands, one per Fiken v2 operation. Run `fiken-cli --help` or `fiken-cli <command> --help` for the live flag list; `fiken-cli which "<capability>"` finds a command by what it does. The hand-built detectors and the write pipeline are in [features.md](features.md).


### account-balances

Manage account balances

- **`fiken-cli account-balances get`** - Retrieves the bookkeeping accounts and closing balances for a given date.
An account is a string with either four digits, or four digits, a colon and five digits ("reskontro").
Examples:
3020 and 1500:10001
- **`fiken-cli account-balances get-companies`** - Retrieves the specified bookkeping account and balance for a given date.

### accounts

Manage accounts

- **`fiken-cli accounts get`** - Retrieves the bookkeeping accounts for the current year
- **`fiken-cli accounts get-companies`** - Retrieves the specified bookkeping account.
An account is a string with either four digits, or four digits, a colon and five digits ("reskontro").
      Examples:
      3020 and 1500:10001

### activities

Manage activities

- **`fiken-cli activities create-activity`** - Creates a new activity
- **`fiken-cli activities delete-activity`** - Delete or archive activity with specified id.

**Behavior:**
- If the activity has no associated time entries and is not set as a default activity on any project, it will be permanently deleted
- If the activity has associated time entries or is set as a default activity on a project, it will be **archived** instead of deleted
- Archived activities are hidden from selection lists but remain accessible for historical time entries

The response is 204 in both cases (deleted or archived).
- **`fiken-cli activities get`** - Returns all activities for given company
- **`fiken-cli activities get-activity`** - Returns activity with specified id
- **`fiken-cli activities update-activity`** - Partially updates activity with provided id. Only provided fields will be updated.

### bank-accounts

Manage bank accounts

- **`fiken-cli bank-accounts create`** - Creates a new bank account. The Location response header returns the URL of the newly created bank account.
Possible types of bank accounts are NORMAL, TAX_DEDUCTION, FOREIGN, and CREDIT_CARD. The field "foreignService" should only be filled out for accounts of type FOREIGN.
- **`fiken-cli bank-accounts get`** - Retrieves all bank accounts associated with the company.
- **`fiken-cli bank-accounts get-companies`** - Retrieves specified bank account.

### bank-balances

Manage bank balances

- **`fiken-cli bank-balances <companySlug>`** - Retrieves all bank balances for the specified company.

### companies

Manage companies

- **`fiken-cli companies get`** - Returns all companies from the system that the user has access to. The user can update which companies a given app has
access to in Fiken under Brukerinnstillinger -> Sikkerhet -> Apper du har gitt tilgang til.
- **`fiken-cli companies get-company`** - Returns company associated with slug.

### contacts

Manage contacts

- **`fiken-cli contacts create`** - Creates a new contact. The Location response header returns the URL of the newly created contact.
- **`fiken-cli contacts delete`** - Deletes the contact if possible (no associated journal entries/sales/invoices/etc). If not possible to delete will set the contact to inactive
- **`fiken-cli contacts get`** - Retrieves all contacts for the specified company.
- **`fiken-cli contacts get-companies`** - Retrieves specified contact. ContactId is returned with a GET contacts call as the first returned field.
ContactId is returned in the Location response header for POST contact.
- **`fiken-cli contacts update`** - Updates an existing contact.

### credit-notes

Manage credit notes

- **`fiken-cli credit-notes add-attachment-to-draft`** - Creates and adds a new attachment to a credit note draft
- **`fiken-cli credit-notes create-counter`** - Creates the first credit note number which is then increased by one with every new credit note. By sending an empty request body the default is base number 10000 (the first credit note number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli credit-notes create-draft`** - Creates a credit note draft. This draft corresponds to a draft for an "uavhengig kreditnota" in Fiken.
- **`fiken-cli credit-notes create-from-draft`** - Creates a credit note from an already created draft.
- **`fiken-cli credit-notes create-full`** - Creates a new credit note that covers the full amount of the associated invoice.
- **`fiken-cli credit-notes create-partial`** - Creates a new credit note that doesn't fully cover the total amount of the associated invoice.
- **`fiken-cli credit-notes delete-draft`** - Delete credit note draft with specified id.
- **`fiken-cli credit-notes get`** - Returns all credit notes for given company
- **`fiken-cli credit-notes get-companies`** - Returns credit note with specified id.
- **`fiken-cli credit-notes get-counter`** - Retrieves the counter for credit notes if it has been created
- **`fiken-cli credit-notes get-draft`** - Returns credit note draft with specified id.
- **`fiken-cli credit-notes get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli credit-notes get-drafts`** - Returns all credit note drafts for given company.
- **`fiken-cli credit-notes send`** - Sends the specified document
- **`fiken-cli credit-notes update-draft`** - Updates credit note draft with provided id.

### general-journal-entries

Manage general journal entries

- **`fiken-cli general-journal-entries <companySlug>`** - Creates a new general journal entry (fri postering).

### groups

Manage groups

- **`fiken-cli groups <companySlug>`** - Returns all customer groups for given company

### inbox

Manage inbox

- **`fiken-cli inbox create-document`** - Upload a document to the inbox
- **`fiken-cli inbox delete-document`** - Removes the inbox document with the specified id from the inbox.
The document is moved to the recycle bin (papirkurv) and is no longer
returned by the inbox listing endpoint. Any underlying file remains
intact, so documents that were attached to a transaction stay attached
to that transaction.
- **`fiken-cli inbox get`** - Returns the contents of the inbox for given company.
- **`fiken-cli inbox get-document`** - Returns the inbox document with specified id

### invoices

Manage invoices

- **`fiken-cli invoices add-attachment-to-draft`** - Creates and adds a new attachment to an invoice draft
- **`fiken-cli invoices create`** - Creates an invoice. This corresponds to "Ny faktura" in Fiken.
There are two types of invoice lines that can be added to an invoice line: product line or free text line.
Provide a product Id if you are invoicing a product. All information regarding the price and VAT for this product will be added to the invoice.
It is however also possible to override the unit amount by sending information in both fields "productId" and "unitAmount".
An invoice line can also be a free text line meaning that no existing product will be associated with the invoiced line.
In this case all information regarding the price and VAT of the product or service to be invoiced must be provided.
- **`fiken-cli invoices create-counter`** - Creates the first invoice number which is then increased by one with every new invoice. By sending an empty request body the default is base number 10000 (the first invoice number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli invoices create-draft`** - Creates an invoice draft.
- **`fiken-cli invoices create-from-draft`** - Creates an invoice from an already created draft.
- **`fiken-cli invoices delete-draft`** - Delete invoice draft with specified id.
- **`fiken-cli invoices get`** - Returns all invoices for given company. You can filter based on issue date, last modified date, customer ID, and if the invoice is settled or not.
- **`fiken-cli invoices get-companies`** - Returns invoice with specified id.
- **`fiken-cli invoices get-counter`** - Retrieves the counter for invoices if it has been created
- **`fiken-cli invoices get-draft`** - Returns invoice draft with specified id.
- **`fiken-cli invoices get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli invoices get-drafts`** - Returns all invoice drafts for given company.
- **`fiken-cli invoices send`** - Sends the specified document
- **`fiken-cli invoices update`** - Updates invoice with provided id. It is possible to update the due date of an invoice
as well as if the invoice was sent manually, outside of Fiken.
- **`fiken-cli invoices update-draft`** - Updates invoice draft with provided id.

### journal-entries

Manage journal entries

- **`fiken-cli journal-entries get`** - Returns all general journal entries (posteringer) for the specified company.
- **`fiken-cli journal-entries get-journal-entry`** - Returns all journal entries within a given company's Journal Entry Service

### offers

Manage offers

- **`fiken-cli offers add-attachment-to-draft`** - Creates and adds a new attachment to an offer draft
- **`fiken-cli offers create-counter`** - Creates the first offer number which is then increased by one with every new offer. By sending an empty request body the default is base number (the first offer number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli offers create-draft`** - Creates an offer draft.
- **`fiken-cli offers create-from-draft`** - Creates an offer from an already created draft.
- **`fiken-cli offers delete-draft`** - Delete offer draft with specified id.
- **`fiken-cli offers get`** - Returns all offers for given company
- **`fiken-cli offers get-companies`** - Returns offer with specified id.
- **`fiken-cli offers get-counter`** - Retrieves the counter for offers if it has been created
- **`fiken-cli offers get-draft`** - Returns offer draft with specified id.
- **`fiken-cli offers get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli offers get-drafts`** - Returns all offer drafts for given company.
- **`fiken-cli offers send`** - Sends the specified document
- **`fiken-cli offers update-draft`** - Updates offer draft with provided id.

### order-confirmations

Manage order confirmations

- **`fiken-cli order-confirmations add-attachment-to-draft`** - Creates and adds a new attachment to an order confirmation draft
- **`fiken-cli order-confirmations create-counter`** - Creates the first order confirmation number which is then increased by one with every new order confirmation. By sending an empty request body the default is base number (the first order confirmation number will thus be 10001), but can be specified to another starting value.
- **`fiken-cli order-confirmations create-draft`** - Creates an order confirmation draft.
- **`fiken-cli order-confirmations create-from-draft`** - Creates an order confirmation from an already created draft.
- **`fiken-cli order-confirmations delete-draft`** - Delete order confirmation draft with specified id.
- **`fiken-cli order-confirmations get`** - Returns all order confirmations for given company
- **`fiken-cli order-confirmations get-companies`** - Returns order confirmation with specified id.
- **`fiken-cli order-confirmations get-counter`** - Retrieves the counter for order confirmations if it has been created
- **`fiken-cli order-confirmations get-draft`** - Returns order confirmation draft with specified id.
- **`fiken-cli order-confirmations get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli order-confirmations get-drafts`** - Returns all order confirmation drafts for given company.
- **`fiken-cli order-confirmations update-draft`** - Updates order confirmation draft with provided id.

### products

Manage products

- **`fiken-cli products create`** - Creates a new product.
- **`fiken-cli products create-sales-report`** - Creates a report based on provided specifications.
- **`fiken-cli products delete`** - Delete product with specified id.
- **`fiken-cli products get`** - Returns all products for given company
- **`fiken-cli products get-companies`** - Returns product with specified id.
- **`fiken-cli products update`** - Updates an existing product.

### projects

Manage projects

- **`fiken-cli projects create`** - Creates a new project
- **`fiken-cli projects delete`** - Delete project with specified id.
- **`fiken-cli projects get`** - Returns all projects for given company
- **`fiken-cli projects get-companies`** - Returns project with specified id.
- **`fiken-cli projects update`** - Updates project with provided id.

### purchases

Manage purchases

- **`fiken-cli purchases add-attachment-to-draft`** - Creates and adds a new attachment to a draft
- **`fiken-cli purchases create`** - Creates a new purchase.
- **`fiken-cli purchases create-draft`** - Creates a purchase draft.
- **`fiken-cli purchases create-from-draft`** - Creates a purchase from an already created draft.
- **`fiken-cli purchases delete-draft`** - Delete draft with specified id.
- **`fiken-cli purchases get`** - Returns all purchases for given company
- **`fiken-cli purchases get-companies`** - Returns purchase with specified id.
- **`fiken-cli purchases get-draft`** - Returns draft with specified id.
- **`fiken-cli purchases get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli purchases get-drafts`** - Returns all purchase drafts for given company.
- **`fiken-cli purchases update-draft`** - Updates draft with provided id.

### sales

Manage sales

- **`fiken-cli sales add-attachment-to-draft`** - Creates and adds a new attachment to a draft
- **`fiken-cli sales create`** - Creates a new sale. This corresponds to "Annet salg" in Fiken and should be used when the invoice document and invoice number have been created outside Fiken. Otherwise the invoices-endpoints should be used.
- **`fiken-cli sales create-draft`** - Creates a sale draft.
- **`fiken-cli sales create-from-draft`** - Creates a sale from an already created draft.
- **`fiken-cli sales delete-draft`** - Delete draft with specified id.
- **`fiken-cli sales get`** - Returns all sales for given company
- **`fiken-cli sales get-companies`** - Returns sale with specified id.
- **`fiken-cli sales get-draft`** - Returns draft with specified id.
- **`fiken-cli sales get-draft-attachments`** - Returns all attachments for specified draft.
- **`fiken-cli sales get-drafts`** - Returns all sale drafts for given company.
- **`fiken-cli sales update-draft`** - Updates draft with provided id.

### time-entries

Manage time entries

- **`fiken-cli time-entries create-invoice-draft-from`** - Creates an invoice draft from one or more time entries.

The time entries will be converted to invoice lines based on the specified grouping.
After successful creation, the included time entries will be marked as "in draft" and
cannot be modified until the draft is deleted or converted to an invoice.

**Grouping options:**
- activity: One invoice line per activity, summing hours across all selected time entries for that activity
- activityAndPerson: One invoice line per unique activity+person combination
- none: Each time entry becomes its own invoice line

**Line description:**
By default, the invoice line description is generated from the activity name and total hours.
If `includeTimeEntryDescriptions` is true, individual time entry descriptions are appended.
- **`fiken-cli time-entries create-time-entry`** - Creates a new time entry
- **`fiken-cli time-entries delete-time-entry`** - Delete time entry with specified id.

**Restrictions:**
- Cannot delete if the time entry has been invoiced
- Cannot delete if the time entry belongs to a closed accounting period

Returns 400 Bad Request if deletion is not allowed due to these restrictions.
- **`fiken-cli time-entries get`** - Returns all time entries for given company
- **`fiken-cli time-entries get-time-entry`** - Returns time entry with specified id
- **`fiken-cli time-entries update-time-entry`** - Partially updates time entry with provided id. Only provided fields will be updated.

### time-users

Manage time users

- **`fiken-cli time-users get`** - Returns all persons who can register time entries for the given company
- **`fiken-cli time-users get-companies`** - Returns time user with specified id

### transactions

Manage transactions

- **`fiken-cli transactions get`** - Returns all transactions for the specified company
- **`fiken-cli transactions get-companies`** - Returns given transaction with associated id. Transaction id is returned in GET calls for
sales, purchases, and journal entries.

### user

Manage user

- **`fiken-cli user`** - Returns information about the user



## Output Formats

```bash
# Human-readable table (default in terminal, JSON when piped)
fiken-cli account-balances get mock-value --date 2026-01-15

# JSON for scripting and agents
fiken-cli account-balances get mock-value --date 2026-01-15 --json

# Filter to specific fields
fiken-cli account-balances get mock-value --date 2026-01-15 --json --select id,name,status

# Dry run — show the request without sending
fiken-cli account-balances get mock-value --date 2026-01-15 --dry-run

# Agent mode — JSON + compact + no prompts in one flag
fiken-cli account-balances get mock-value --date 2026-01-15 --agent
```

## Agent Usage

This CLI is designed for AI agent consumption:

- **Non-interactive** - never prompts, every input is a flag
- **Pipeable** - `--json` output to stdout, errors to stderr
- **Filterable** - `--select id,name` returns only fields you need
- **Previewable** - `--dry-run` shows the request without sending
- **Explicit retries** - add `--idempotent` to create retries and `--ignore-missing` to delete retries when a no-op success is acceptable
- **Confirmable** - `--yes` for explicit confirmation of destructive actions
- **Piped input** - write commands can accept structured input when their help lists `--stdin`
- **Offline-friendly** - sync/search commands can use the local SQLite store when available
- **Agent-safe by default** - no colors or formatting unless `--human-friendly` is set

Exit codes: `0` success, `2` usage error, `3` not found, `4` auth error, `5` API error, `7` rate limited, `8` write refused by the test-company guard (nothing was sent), `10` config error.
