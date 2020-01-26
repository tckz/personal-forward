


```json
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
            "body": "{some json or other content}"
          }   
        }   
      ] 
    } 
  ]
}
```