package invoicing

// LinkOrderInvoice binds an order to the document issued for it.
//
// The definition itself is declared by the INVOICE module (ADR 0005: by the
// side that writes the record the binding carries). The name is repeated here
// as a literal rather than imported, for the same reason the other flows repeat
// module names: reaching into the module for a constant would tie this package
// to it at compile time for the sake of a string.
const LinkOrderInvoice = "order_invoice"
