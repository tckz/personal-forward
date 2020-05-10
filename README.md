personal-forward
===

Expose your local web server to the internet.  

Accept https request from the internet and forward it to local web server.
Its response is returned to the requester.

# Requirements

* Firestore
* GAE/Go
* go 1.12
* GNU make
* git

# Prerequisite

* GCP: Create GCP project.
* Firebase: Create Firebase project with GCP project associated.
* Firebase: Create Cloud Firestore Database.

# Deployment

* GAE: Deploy forwarder application to GAE.
  ```bash
  $ cat app-example.yaml
  runtime: go112
  service: {default|your service name}
  main: github.com/tckz/personal-forward/cmd/forwarder
  $ gcloud app deploy app-example.yaml
  ```
  * Caution: First GAE service must be 'default'
* (If required)GAE: Attach firewall rules to GAE app.
* (If required)GAE: Grant user access to the service using Identity-Aware Proxy.
* Build
  ```bash
  $ make
  ```
* (Optional)Export environment variables if necessary.
  ```bash
  $ GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json
  ```
* Connect local consumer to the GAE app via Firestore and start forwarding.
  ```bash
  $ ./dist/forward-consumer --target http://localhost:3010 --endpoint-name default
  ```

# Modules

## forwarder

* GAE application.
* Accept https request from the internet.
* Create Firestore document represents the accepted request and add it to collection.
* Receive response from forward-consumer and respond it to original requester.

## forward-consumer

* Listening Firestore collection which represents requests. 
* Forward http request to local web server and receive its response and write it to the Firestore document.

```
Usage: forward-consumer [options]

  -dump
        Dump received request or not
  -dump-forward
        Dump forward request and response
  -endpoint-name string
        Identity of endpoint
  -expire duration
        Ignore too old request (default 2m0s)
  -forward-timeout duration
        Timeout for forwarding http request (default 30s)
  -json-key string
        /path/to/servicekey.json
  -max-dump-bytes uint
        Size condition for determine whether dump body of request/response or not. (default 4096)
  -pattern value
        Path pattern for target.
  -target value
        URL of forwarding target.
  -version
        Show version
  -without-cleaning
        Delete request documents that is expired
  -workers int
        Number of groutines to process request (default 8)
```

# Development

## Firestore document structure

* `@something` indicates collection.
* `$Id$` indicates ID of the document.

```json5
{
  "@endpoints": [
    {
      "$id$": "someendpointname",
      "@requests": [
        {
          "$id$": "e0948a8aQLf38g6AveBaGClx2D0JlyrGYa_Ux-XPQQk",
          "created": "2020-01-26T16:37:12.340+0900",
          "request": {
            "httpInfo": {
              "method": "GET",
              "requestURI": "/path/to/some?xxx=bbb"
            },
            "header": {
              "content-type": ["application/json"],
              "host": ["localhost:3010"]
            },
            "body": "{some json or other content}"
          },
          "response": {
            "time": "2020-01-26T16:37:12.340+0900",
            "statusCode": 200,
            "header": {
              "content-type": ["application/json"],
              "content-length": ["1234"]
            },
            // Set when responseBodies is exist. Indicates number of docs of responseBodies.
            "chunks": 2,
            "body": "{some json or other content}"
          },
          // responseBodies only appears when response size over 1MB.
          "@responseBodies" : [
            {
              "$id$": "e0948a8aQLf38g6AveBaGClx2D0JlyrGYa_Ux-XPQQk",
              "index": 0,
              "chunk": "[]byte of chunk",
              "size": 123456
            },
            {
              "$id$": "e0948a8aQLf38g6AveBaGClx2D0JlyrGYa_Ux-XPQQk",
              "index": 1,
              "chunk": "[]byte of chunk",
              "size": 123456
            }
          ]
        }   
      ] 
    } 
  ]
}
```

# License

BSD 2-Clause License

SEE LICENSE
