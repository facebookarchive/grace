go.grace [![Build Status](https://secure.travis-ci.org/daaku/go.grace.png)](http://travis-ci.org/daaku/go.grace)
========

Package grace provides a library that makes it easy to build socket
based servers that can be gracefully terminated & restarted (that is,
without dropping any connections).

Demo HTTP Server with graceful termination and restart:
https://github.com/daaku/go.grace/blob/master/gracedemo/demo.go

http level graceful termination and restart:
http://go.pkgdoc.org/github.com/daaku/go.grace/gracehttp

net.Listener level graceful termination and restart:
http://go.pkgdoc.org/github.com/daaku/go.grace
