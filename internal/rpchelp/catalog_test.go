package rpchelp

import "encoding/json"

func exampleCatalog() *Catalog {
	return &Catalog{
		Actions: map[string]operation{
			"getWorkbook": {Schema: json.RawMessage(`{
				"properties":{"workbookId":{"type":"string"},"includePermissionsInfo":{"type":"boolean"}},
				"required":["workbookId"]
			}`)},
			"getDashboard": {
				Schema: json.RawMessage(`{
					"properties":{"dashboardId":{"type":"string"},"branch":{"$ref":"#/$defs/Branch"}},
					"required":["dashboardId"]
				}`),
				Definitions: []string{"Branch"},
			},
			"createDashboard": {Schema: json.RawMessage(`{
				"properties":{"entry":{"properties":{"meta":{"type":"object"},"data":{"type":"object"}},"required":["meta","data"]}},
				"required":["entry"]
			}`)},
			"createWizardChart": {Schema: json.RawMessage(`{
				"properties":{"data":{"type":"object"},"annotation":{"type":"string"}},"required":["data"]
			}`)},
			"updateHtmlPage": {Schema: json.RawMessage(`{
				"properties":{"entryId":{"type":"string"}},"required":["entryId"],
				"anyOf":[
					{"properties":{"revId":{"type":"string"},"mode":{"const":"publish"}},"required":["revId","mode"]},
					{"properties":{"content":{"type":"string"}},"required":["content"]}
				]
			}`)},
			"getEntriesPermissions": {Schema: json.RawMessage(`{
				"properties":{"entryIds":{"type":"array","items":{"type":"string"}}},
				"required":["entryIds"],"additionalProperties":false
			}`)},
			"createConnection": {Schema: json.RawMessage(`{
				"properties":{"name":{"type":"string"}},"required":["name"],
				"oneOf":[
					{"properties":{"type":{"const":"clickhouse"},"host":{"type":"string"}},"required":["type","host"]},
					{"properties":{"type":{"const":"starrocks"},"username":{"type":"string"}},"required":["type","username"]},
					{"properties":{"type":{"const":"json_api"},"allowed_methods":{"type":"array","items":{"enum":["PATCH"]}}},"required":["type","allowed_methods"]}
				]
			}`)},
			"updateConnection": {Schema: json.RawMessage(`{
				"properties":{"connectionId":{"type":"string"},"data":{"properties":{"password":{"type":"string"}},"additionalProperties":false}},
				"required":["connectionId","data"]
			}`)},
			"getAuditEntriesUpdates": {Schema: json.RawMessage(`{
				"properties":{"from":{"type":"string","format":"date-time"}},"required":["from"]
			}`)},
			"setLicenseLimit": {Schema: json.RawMessage(`{
				"properties":{"value":{"type":"number","maximum":10000}},"required":["value"]
			}`)},
		},
		Schemas: map[string]json.RawMessage{"Branch": json.RawMessage(`{"enum":["published","draft"]}`)},
	}
}
