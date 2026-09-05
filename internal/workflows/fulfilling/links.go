package fulfilling

// LinkOrderFulfillment binds an order to the shipments opened for it.
//
// The definition itself is declared by the FULFILLMENT module (ADR 0005: by the
// side that writes the record the binding carries). The name is repeated here
// as a literal rather than imported, for the same reason the other flows repeat
// module names: reaching into the module for a constant would tie this package
// to it at compile time for the sake of a string.
const LinkOrderFulfillment = "order_fulfillment"
