
.. image:: docs/_static/logo_with_text.svg
   :alt: Gazette Logo

Overview
=========

Gazette makes it easy to build platforms that flexibly mix *SQL*, *batch*,
and *millisecond-latency streaming* processing paradigms. It enables teams,
applications, and analysts to work from a common catalog of data in the way
that's most convenient **to them**. Gazette's core abstraction is a "journal"
-- a streaming append log that's represented using regular files in a BLOB
store (i.e., S3).

The magic of this representation is that journals are simultaneously a
low-latency data stream *and* a collection of immutable, organized files
in cloud storage (aka, a data lake) -- a collection which can be directly
integrated into familiar processing tools and SQL engines.

Atop the journal *broker service*, Gazette offers a powerful *consumers
framework* for building streaming applications in Go. Gazette has served
production use cases for nearly five years, with deployments scaled to
millions of streamed records per second.

**This is not the official Gazette repository.**
`Gazette is developed at github.com/gazette/core <https://github.com/gazette/core>`_.
