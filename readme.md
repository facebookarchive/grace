go.grace [![Build Status](https://secure.travis-ci.org/daaku/go.grace.png)](http://travis-ci.org/daaku/go.grace)
========

Package grace provides a library that makes it easy to build socket
based servers that can be gracefully terminated & restarted (that is,
without dropping any connections).

It provides a convenient API for HTTP servers, especially if you need to listen
on multiple ports (for example a secondary internal only admin server).
Additionally it is implemented using the same API as systemd providing [socket
activation](http://0pointer.de/blog/projects/socket-activation.html)
compatibility to also provide lazy activation of the server.

Demo HTTP Server with graceful termination and restart:
https://github.com/daaku/go.grace/blob/master/gracedemo/demo.go

http level graceful termination and restart:
http://godoc.org/github.com/daaku/go.grace/gracehttp

net.Listener level graceful termination and restart:
http://godoc.org/github.com/daaku/go.grace
