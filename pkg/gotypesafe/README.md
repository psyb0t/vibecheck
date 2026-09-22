# gotypesafe

`gotypesafe` is the public Go client for TypeSafe's API. The package name is `typesafe`. Jev is the default model, not the API name.

The generated wire types and HTTP client come from TypeSafe's published OpenAPI document. The handwritten layer adds bounded concurrency, timeouts, response-size limits, retry classification, normalized typed answers, and a small interface that Mockery can implement.

## Use it

```go
import typesafe "github.com/psyb0t/vibecheck/pkg/gotypesafe"

client, err := typesafe.New(typesafe.Config{
    APIKey: "your-typesafe-api-key-here",
})
if err != nil {
    return err
}

response, err := client.Evaluate(ctx, typesafe.Request{
    State: map[string]any{
        "message": "I was charged twice for one invoice.",
    },
    Questions: map[string]typesafe.QuestionInput{
        "destination": {
            Type:         typesafe.QuestionTypeChoice,
            Instructions: "Which support queue should receive this message?",
            Criteria: map[string]any{
                "billing":   "Charges, invoices, payments, and refunds",
                "technical": "Bugs, outages, setup, and integrations",
                "other":     "Anything else",
            },
        },
    },
})
```

`response.Answers["destination"]` contains the selected choice, confidence, and full probability distribution. `response.Model` contains the versioned model that answered. Call `Models` to list the models available to the configured credential.

## Refresh generated bindings

```bash
make typesafe-openapi-update
make generate
make test-gotypesafe
```

`openapi.json` is the checked-in upstream snapshot. `api.gen.go` and `mocks/client.gen.go` are generated and must not be edited by hand.
