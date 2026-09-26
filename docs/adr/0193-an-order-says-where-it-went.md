# ADR 0193 — An order says where it went

**Summary:** The operator's order record carries the shipping and billing
addresses the order was placed with, and an invoice's buyer is filled from the
billing address where the request leaves it empty. It costs a second record
shape on the admin surface, and it gives the addresses B11 kept the readers B11
named (D140).

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0193](../measurements/0193-an-address-nobody-read.md)

## Context

Since B11 an order keeps the two addresses its cart carried, so that it could
say where it went, an invoice could print a buyer and a label had a
destination. None of the three read them. The admin order record had no address
field. The invoice surface, written that morning, said it carried "no
addresses", and the invoicing flow took every buyer field but the e-mail from
the request. A parcel's carrier gets only what the operator types into it.

## Decision

The admin order record, read and after each transition, carries
`shipping_address` and `billing_address`, and the storefront's record of the
same order does not. The order's invoice surface carries its billing address,
and an invoice's buyer name, address and country that the request leaves empty
are filled from it, each on its own, as the e-mail already was.

## Consequences

An operator can read the destination and the buyer off the order they are
shipping and invoicing, the address's own `metadata` included.

The storefront reads an order by an id with a key that names the shop (ADR
0008), so it gets no address.

The buyer's name is the billing company when it names one, and the person
otherwise. The address prints as the street lines and then the postal code,
city and province on one line. A request that sends a field keeps it, and a
buyer's tax number and office still come only from the request.

Only the billing address fills a buyer. The shipping address may name a gift's
recipient, and an order with no billing address fills none of the three, so
the invoice module refuses a nameless buyer as it did.

After an erasure both addresses hold what the erasure keeps, the country and
the free metadata. The record shows that, and an invoice issued afterwards gets
a country and no name.

The carrier still receives no destination. That needs the provider contract in
`core/provider` to carry one and is its own record.

## Rejected

- **Addresses on the storefront's order read.** Anyone holding the id reads it,
  and a home address is more than that read should hand them.
- **The shipping address as the buyer when there is no billing address.** A
  document would name a gift's recipient as the buyer.
- **The address as one formatted string on the admin record.** The operator's
  label, a carrier and a document each lay it out their own way.
- **Filling the buyer only when the whole party is empty.** The caller who knows
  the legal name and not the address would have to send both.
