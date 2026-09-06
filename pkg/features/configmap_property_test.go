package features

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func genPropString(ht *hegel.T, alphabet []string, minLen, maxLen int) string {
	length := hegel.Draw(ht, hegel.Integers(minLen, maxLen))
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(alphabet))
	}
	return res
}

func TestBuildFlagdJSON_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		numOverrides := hegel.Draw(ht, hegel.Integers(0, 8))
		overrides := make(map[string]string, numOverrides)

		keyAlphabet := []string{"a", "b", "c", "x", "y", "z", "_"}
		for i := 0; i < numOverrides; i++ {
			key := genPropString(ht, keyAlphabet, 1, 10)
			valType := hegel.Draw(ht, hegel.SampledFrom([]string{"bool_true", "bool_false", "int", "float", "string"}))

			var val string
			switch valType {
			case "bool_true":
				val = "true"
			case "bool_false":
				val = "false"
			case "int":
				val = strconv.Itoa(hegel.Draw(ht, hegel.Integers(-1000, 1000)))
			case "float":
				val = fmt.Sprintf("%.2f", float64(hegel.Draw(ht, hegel.Integers(0, 1000)))/10.0)
			case "string":
				val = genPropString(ht, []string{"h", "e", "l", "l", "o", "w", "o", "r", "l", "d"}, 1, 15)
			}
			overrides[key] = val
		}

		useEnv := hegel.Draw(ht, hegel.Booleans())
		var envName string
		if useEnv {
			envName = genPropString(ht, []string{"e", "n", "v", "-", "1", "2"}, 1, 10)
		}

		data, err := BuildFlagdJSON(overrides, envName)
		if err != nil {
			ht.Fatalf("BuildFlagdJSON failed: %v", err)
		}

		var def FlagdDefinition
		err = json.Unmarshal(data, &def)
		if err != nil {
			ht.Fatalf("failed to unmarshal generated flagd json: %v", err)
		}

		if len(def.Flags) != len(overrides) {
			ht.Fatalf("expected %d flags, got %d", len(overrides), len(def.Flags))
		}

		for k, v := range overrides {
			flag, exists := def.Flags[k]
			if !exists {
				ht.Fatalf("missing flag %s", k)
			}
			if flag.State != "ENABLED" {
				ht.Errorf("expected ENABLED, got %s", flag.State)
			}

			if envName != "" {
				if flag.Targeting == nil {
					ht.Fatalf("expected targeting rule for flag %s with envName %s", k, envName)
				}
				targetingMap, ok := flag.Targeting.(map[string]interface{})
				if !ok {
					ht.Fatalf("targeting should be map[string]interface{}")
				}
				ifList, ok := targetingMap["if"].([]interface{})
				if !ok || len(ifList) != 3 {
					ht.Fatalf("targeting['if'] should be slice of 3 items")
				}

				targetVariant := ifList[1].(string)
				fallbackVariant := ifList[2].(string)

				if flag.DefaultVariant != fallbackVariant {
					ht.Errorf("expected defaultVariant %s, got %s", fallbackVariant, flag.DefaultVariant)
				}

				switch v {
				case "true":
					assert.Equal(t, "on", targetVariant)
					assert.Equal(t, "off", fallbackVariant)
				case "false":
					assert.Equal(t, "off", targetVariant)
					assert.Equal(t, "on", fallbackVariant)
				default:
					assert.Equal(t, "value", targetVariant)
					assert.Equal(t, "default", fallbackVariant)
				}
			} else {
				if flag.Targeting != nil {
					ht.Fatalf("expected nil targeting when envName is empty for flag %s", k)
				}
				switch v {
				case "true":
					require.Equal(t, "on", flag.DefaultVariant)
				case "false":
					require.Equal(t, "off", flag.DefaultVariant)
				default:
					require.Equal(t, "value", flag.DefaultVariant)
				}
			}
		}
	})
}
