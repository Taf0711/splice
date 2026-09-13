# HTTP error envelope

Every non-2xx response from the session service carries a JSON body with one
top-level key, `error`, whose value is an object:

    {
      "error": {
        "status": 404,
        "code": "SESSION_NOT_FOUND",
        "message": "session not found"
      }
    }

`status` repeats the HTTP status code as a number. `code` is a stable,
upper-snake-case identifier. `message` is human-readable and is never parsed by
a client.

## Codes

| HTTP status | code               |
| ---         | ---                |
| 400         | INVALID_ARGUMENT   |
| 404         | SESSION_NOT_FOUND  |
| 405         | METHOD_NOT_ALLOWED |

Clients read `error.code`; they never match on `message`.
