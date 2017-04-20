package signalform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform/helper/schema"
	"io/ioutil"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// Workaround for Signalfx bug related to post processing and lastUpdatedTime
	OFFSET        = 10000.0
	CHART_API_URL = "https://api.signalfx.com/v2/chart"

	// Colors
	GRAY       = "#999999"
	BLUE       = "#0077c2"
	NAVY       = "#6CA2B7"
	ORANGE     = "#b04600"
	YELLOW     = "#e5b312"
	MAGENTA    = "#bd468d"
	PURPLE     = "#e9008a"
	VIOLET     = "#876ffe"
	LILAC      = "#a747ff"
	GREEN      = "#05ce00"
	AQUAMARINE = "#0dba8f"
)

/*
  Utility function that wraps http calls to SignalFx
*/
func sendRequest(method string, url string, token string, payload []byte) (int, []byte, error) {
	client := &http.Client{}

	req, err := http.NewRequest(method, url, bytes.NewReader(payload))
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-SF-Token", token)

	resp, err := client.Do(req)
	if err != nil {
		return -1, nil, fmt.Errorf("Failed sending %s request to Signalfx: %s", method, err.Error())
	}

	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()

	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("Failed reading response body from %s request: %s", method, err.Error())
	}

	return resp.StatusCode, body, nil
}

/*
  Validates the color_range field against a list of allowed words.
*/
func validateChartColor(v interface{}, k string) (we []string, errors []error) {
	value := v.(string)
	if value != "gray" && value != "blue" && value != "navy" && value != "orange" && value != "yellow" && value != "magenta" && value != "purple" && value != "violet" && value != "lilac" && value != "green" && value != "aquamarine" {
		errors = append(errors, fmt.Errorf("%s not allowed; must be either gray, blue, navy, orange, yellow, magenta, purple, violet, lilac, green, aquamarine", value))
	}
	return
}

/*
  Validates the time_span_type field against a list of allowed words.
*/
func validateTimeSpanType(v interface{}, k string) (we []string, errors []error) {
	value := v.(string)
	if value != "relative" && value != "absolute" {
		errors = append(errors, fmt.Errorf("%s not allowed; must be either relative or absolute", value))
	}
	return
}

/*
  Validates that sort_by field start with either + or -.
*/
func validateSortBy(v interface{}, k string) (we []string, errors []error) {
	value := v.(string)
	if !strings.HasPrefix(value, "+") && !strings.HasPrefix(value, "-") {
		errors = append(errors, fmt.Errorf("%s not allowed; must start either with + or - (ascending or descending)", value))
	}
	return
}

/*
	Get Color Scale Options
*/
func getColorScaleOptions(d *schema.ResourceData) map[string]interface{} {
	item := make(map[string]interface{})
	colorScale := d.Get("color_scale").(*schema.Set).List()[0]
	options := colorScale.(map[string]interface{})

	thresholdsList := options["thresholds"].([]interface{})
	thresholds := make([]int, len(thresholdsList))
	for i := range thresholdsList {
		thresholds[i] = thresholdsList[i].(int)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(thresholds)))
	item["thresholds"] = thresholds
	item["inverted"] = options["inverted"].(bool)
	return item
}

/*
  Send a GET to get the current state of the resource. It just checks if the lastUpdated timestamp is
  later than the timestamp saved in the resource. If so, the resource has been modified in some way
  in the UI, and should be recreated. This is signaled by setting synced to false, meaning if synced is set to
  true in the tf configuration, it will update the resource to achieve the desired state.
*/
func resourceRead(url string, sfxToken string, d *schema.ResourceData) error {
	status_code, resp_body, err := sendRequest("GET", url, sfxToken, nil)
	if status_code == 200 {
		mapped_resp := map[string]interface{}{}
		err = json.Unmarshal(resp_body, &mapped_resp)
		if err != nil {
			return fmt.Errorf("Failed unmarshaling for the resource %s during read: %s", d.Get("name"), err.Error())
		}
		// This implies the resource was modified in the Signalfx UI and therefore it is not synced with Signalform
		last_updated := mapped_resp["lastUpdated"].(float64)
		if last_updated > (d.Get("last_updated").(float64) + OFFSET) {
			d.Set("synced", false)
			d.Set("last_updated", last_updated)
		}
	} else {
		if strings.Contains(string(resp_body), "Resource not found") {
			// This implies that the resouce was deleted in the Signalfx UI and therefore we need to recreate it
			d.SetId("")
		} else {
			return fmt.Errorf("For the resource %s SignalFx returned status %d: \n%s", d.Get("name"), status_code, resp_body)
		}
	}

	return nil
}

/*
  Fetches payload specified in terraform configuration and creates a resource
*/
func resourceCreate(url string, sfxToken string, payload []byte, d *schema.ResourceData) error {
	status_code, resp_body, err := sendRequest("POST", url, sfxToken, payload)
	if status_code == 200 {
		mapped_resp := map[string]interface{}{}
		err = json.Unmarshal(resp_body, &mapped_resp)
		if err != nil {
			return fmt.Errorf("Failed unmarshaling for the resource %s during creation: %s", d.Get("name"), err.Error())
		}
		d.SetId(fmt.Sprintf("%s", mapped_resp["id"].(string)))
		d.Set("last_updated", mapped_resp["lastUpdated"].(float64))
		d.Set("synced", true)
	} else {
		return fmt.Errorf("For the resource %s SignalFx returned status %d: \n%s", d.Get("name"), status_code, resp_body)
	}
	return nil
}

/*
  Fetches payload specified in terraform configuration and creates chart
*/
func resourceUpdate(url string, sfxToken string, payload []byte, d *schema.ResourceData) error {
	status_code, resp_body, err := sendRequest("PUT", url, sfxToken, payload)
	if status_code == 200 {
		mapped_resp := map[string]interface{}{}
		err = json.Unmarshal(resp_body, &mapped_resp)
		if err != nil {
			return fmt.Errorf("Failed unmarshaling for the resource %s during creation: %s", d.Get("name"), err.Error())
		}
		// If the resource was updated successfully with Signalform configs, it is now synced with Signalfx
		d.Set("synced", true)
		d.Set("last_updated", mapped_resp["lastUpdated"].(float64))
	} else {
		return fmt.Errorf("For the resource %s SignalFx returned status %d: \n%s", d.Get("name"), status_code, resp_body)
	}
	return nil
}

/*
  Deletes a resource.  If the resource does not exist, it will receive a 404, and carry on as usual.
*/
func resourceDelete(url string, sfxToken string, d *schema.ResourceData) error {
	status_code, resp_body, err := sendRequest("DELETE", url, sfxToken, nil)
	if err != nil {
		return fmt.Errorf("Failed deleting resource  %s: %s", d.Get("name"), err.Error())
	}
	if status_code < 400 || status_code == 404 {
		d.SetId("")
	} else {
		return fmt.Errorf("For the resource  %s SignalFx returned status %d: \n%s", d.Get("name"), status_code, resp_body)
	}
	return nil
}

/*
	Util method to get Legend Chart Options.
*/
func getLegendOptions(d *schema.ResourceData) map[string]interface{} {
	if properties, ok := d.GetOk("legend_fields_to_hide"); ok {
		properties := properties.(*schema.Set).List()
		legendOptions := make(map[string]interface{})
		properties_opts := make([]map[string]interface{}, len(properties))
		for i, property := range properties {
			property := property.(string)
			item := make(map[string]interface{})
			item["property"] = property
			item["enabled"] = false
			properties_opts[i] = item
		}
		if len(properties_opts) > 0 {
			legendOptions["fields"] = properties_opts
			return legendOptions
		}
	}
	return nil
}

/*
	Util method to validate SignalFx specific string format.
*/
func validateSignalfxRelativeTime(v interface{}, k string) (we []string, errors []error) {
	ts := v.(string)

	r, _ := regexp.Compile("-([0-9]+)[mhdw]")
	if !r.MatchString(ts) {
		errors = append(errors, fmt.Errorf("%s not allowed. Please use milliseconds from epoch or SignalFx time syntax (e.g. -5m, -1h)", ts))
	}
	return
}

/*
*  Util method to convert from Signalfx string format to milliseconds
 */
func fromRangeToMilliSeconds(timeRange string) (int, error) {
	r := regexp.MustCompile("-([0-9]+)([mhdw])")
	ss := r.FindStringSubmatch(timeRange)
	var c int
	switch ss[2] {
	case "m":
		c = 60 * 1000
	case "h":
		c = 60 * 60 * 1000
	case "d":
		c = 24 * 60 * 60 * 1000
	case "w":
		c = 7 * 24 * 60 * 60 * 1000
	default:
		c = 1
	}
	val, err := strconv.Atoi(ss[1])
	if err != nil {
		return -1, err
	}
	return val * c, nil
}
