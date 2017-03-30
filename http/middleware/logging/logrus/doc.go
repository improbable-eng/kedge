// Copyright 2017 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

/*
`http_logrus` is a HTTP logging middleware for the Logrus logging stack.

It accepts a user-configured `logrus.Entry` that will be used for logging completed HTTP calls. The same
`logrus.Entry` will be used for logging completed gRPC calls, and be populated into the `context.Context` passed into HTTP handler code.

You can use `Extract` to log into a request-scoped `logrus.Entry` instance in your handler code. `AddFields` adds new fields
to the request-scoped `logrus.Entry`. They will be propagated for all call depending on the context, including the
interceptor's own "finished handling" log message.

Logrus can also be made as a backend for HTTP library internals. For that use `AsHttpLogger`.

Please see examples and tests for examples of use.
*/
package http_logrus
