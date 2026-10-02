# ADR 0363 — A request body requires no field

**Summary:** The served document's request bodies list no required field,
since the decoder accepts every field absent and the service refuses what a
body must carry; a type also written in a response is published a second
time, as read, under its name with `Input`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

`core/openapi` took a field without `omitempty` for a required one. That is
true of a response, whose encoder always writes the field, and it was applied
to request bodies alike, where the decoder accepts the field absent. A client
generated from `/openapi.json` was made to send every field of a body: it
could not open a guest cart without naming a customer and an order to add
to. A type read from a request and written in a response was one component
for both.

## Decision

A request body's schema is its type's request form, in which no field is
required, and what a body must carry the service refuses by a code and a
reason. A type both read and written is published twice, as written under
its name and as read under its name with `Input`, the names decided when the
document is built.

## Consequences

- A generated client builds a body from the fields it means to send; a
  field the service needs and the client leaves out is refused with its
  reason.
- What a body must carry is in the operation's description and the refusal,
  not in the schema.
- Responses are unchanged: a field without `omitempty` is required there.
- The request forms of the five types this repository both reads and writes
  are renamed for an integrator, each to its name with `Input`; a client
  generated before refers to the old names in its request types.
- An embedder's module gets the same through `Doc.RequestBody`. A body built
  from `Doc.SchemaOf` is described in the written form, which is how the
  payment module's optional bodies were described until this change.
- A name belongs to one type across both forms, and a type already holding
  a request form's `Input` name stops the document as any clash does.

## Rejected

- A tag marking the fields a service refuses absent: nothing would hold it
  true, and a tag on a field the service accepts absent is the same false
  claim the other way round. It may come with a gate that sends the body
  without the field.
- Splitting the five shared Go types: it mends this repository and leaves
  an embedder's shared type described wrongly.
- Naming the request form when its type registers: one operation literal
  derives its body before its response, so the written form can register
  after the body already refers to the name.
- Dropping `required` from every component: a response would claim that
  fields it always writes may be absent.
