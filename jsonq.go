package gojsonq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
)

// New returns a new instance of JSONQ
func New() *JSONQ {
	return &JSONQ{
		queryMap: loadDefaultQueryMap(),
	}
}

// empty represents an empty result
var empty interface{}

// query describes a query
type query struct {
	key, operator string
	value         interface{}
}

// JSONQ describes a JSONQ type which contains all the state
type JSONQ struct {
	queryMap        map[string]QueryFunc
	node            string
	raw             json.RawMessage // raw message from source (reader, string or file)
	rootJSONContent interface{}     // original decoded json data
	jsonContent     interface{}     // copy of original decoded json data for further processing
	queryIndex      int
	queries         []([]query) // nested queries
	attributes      []string    // select attributes
	errors          []error     // contains all the errors when processing
}

// String statisfy stringer interface
func (j *JSONQ) String() string {
	return fmt.Sprintf("\nContent: %s\nQueries:%v\n", string(j.raw), j.queries)
}

// decode decodes the raw message to Go data structure
func (j *JSONQ) decode() *JSONQ {
	err := json.Unmarshal(j.raw, &j.rootJSONContent)
	if err != nil {
		return j.addError(err)
	}
	j.jsonContent = j.rootJSONContent
	return j
}

// Copy returns a new fresh instance of JSONQ with the original copy of data so that you can do
// concurrent operation on the same data without being decoded again
func (j *JSONQ) Copy() *JSONQ {
	tmp := *j
	return tmp.reset()
}

// File read the json content from physical file
func (j *JSONQ) File(filename string) *JSONQ {
	bb, err := ioutil.ReadFile(filename)
	if err != nil {
		return j.addError(err)
	}
	j.raw = bb
	return j.decode() // handle error
}

// JSONString reads the json content from valid json string
func (j *JSONQ) JSONString(json string) *JSONQ {
	j.raw = []byte(json)
	return j.decode() // handle error
}

// Reader reads the json content from io reader
func (j *JSONQ) Reader(r io.Reader) *JSONQ {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r)
	if err != nil {
		return j.addError(err)
	}
	j.raw = buf.Bytes()
	buf.Reset() // reset the buffer
	return j.decode()
}

// Error returns last error
func (j *JSONQ) Error() error {
	errsln := len(j.errors)
	if errsln == 0 {
		return nil
	}
	return j.errors[errsln-1]
}

// Errors returns list of all errors
func (j *JSONQ) Errors() []error {
	return j.errors
}

// addError adds error to error list
func (j *JSONQ) addError(err error) *JSONQ {
	j.errors = append(j.errors, fmt.Errorf("gojsonq: %v", err))
	return j
}

// Macro adds a new query func to the JSONQ
func (j *JSONQ) Macro(operator string, fn QueryFunc) *JSONQ {
	if _, ok := j.queryMap[operator]; ok {
		j.addError(fmt.Errorf("%s is already registered in query map", operator))
	}
	j.queryMap[operator] = fn
	return j
}

// From seeks the json content to provided node. e.g: "users.[0]"  or "users.[0].name"
func (j *JSONQ) From(node string) *JSONQ {
	j.node = node
	return j.findNode(node)
}

// findNode seeks the json content to provided node and assign it to the jsonContent property
func (j *JSONQ) findNode(node string) *JSONQ {
	pp := strings.Split(node, ".")
	for _, n := range pp {
		if isIndex(n) {
			// find slice/array
			if arr, ok := j.jsonContent.([]interface{}); ok {
				indx, err := getIndex(n)
				if err != nil {
					return j.addError(err)
				}
				arrLen := len(arr)
				if arrLen == 0 ||
					indx > arrLen-1 {
					j.jsonContent = empty
					// TODO: need to send error
					return j
				}
				j.jsonContent = arr[indx]
			}
		} else {
			// find in map
			invalidNode := true
			if mp, ok := j.jsonContent.(map[string]interface{}); ok {
				j.jsonContent = mp[n]
				invalidNode = false
			}

			// find in group data
			if mp, ok := j.jsonContent.(map[string][]interface{}); ok {
				j.jsonContent = mp[n]
				invalidNode = false
			}

			if invalidNode {
				j.jsonContent = empty
				j.addError(fmt.Errorf("invalid node name %s", n))
			}
		}
	}
	return j
}

// Select select the properties from query result
func (j *JSONQ) Select(properties ...string) *JSONQ {
	j.attributes = append(j.attributes, properties...)
	return j
}

// Where builds a where clause. e.g: Where("name", "contains", "doe")
func (j *JSONQ) Where(key, cond string, val interface{}) *JSONQ {
	q := query{
		key:      key,
		operator: cond,
		value:    val,
	}
	if j.queryIndex == 0 && len(j.queries) == 0 {
		qq := []query{}
		qq = append(qq, q)
		j.queries = append(j.queries, qq)
	} else {
		j.queries[j.queryIndex] = append(j.queries[j.queryIndex], q)
	}

	return j
}

// WhereEqual is an alias of Where("key", "=", val)
func (j *JSONQ) WhereEqual(key string, val interface{}) *JSONQ {
	return j.Where(key, "=", val)
}

// WhereNotEqual is an alias of Where("key", "!=", val)
func (j *JSONQ) WhereNotEqual(key string, val interface{}) *JSONQ {
	return j.Where(key, "!=", val)
}

// WhereNil is an alias of Where("key", "=", nil)
func (j *JSONQ) WhereNil(key string) *JSONQ {
	return j.Where(key, "=", nil)
}

// WhereNotNil is an alias of Where("key", "!=", nil)
func (j *JSONQ) WhereNotNil(key string) *JSONQ {
	return j.Where(key, "!=", nil)
}

// WhereIn is an alias for where('key', 'in', []string{"a", "b"})
func (j *JSONQ) WhereIn(key string, val interface{}) *JSONQ {
	j.Where(key, signIn, val)
	return j
}

// WhereNotIn is an alias for where('key', 'notIn', []string{"a", "b"})
func (j *JSONQ) WhereNotIn(key string, val interface{}) *JSONQ {
	j.Where(key, signNotIn, val)
	return j
}

// OrWhere builds an OrWhere clause, basically it's a group of AND clauses
func (j *JSONQ) OrWhere(key, cond string, val interface{}) *JSONQ {
	j.queryIndex++
	qq := []query{}
	qq = append(qq, query{
		key:      key,
		operator: cond,
		value:    val,
	})
	j.queries = append(j.queries, qq)
	return j
}

// WhereStartsWith satisfies Where clause which starts with provided value(string)
func (j *JSONQ) WhereStartsWith(key string, val interface{}) *JSONQ {
	return j.Where(key, signStartsWith, val)
}

// WhereEndsWith satisfies Where clause which ends with provided value(string)
func (j *JSONQ) WhereEndsWith(key string, val interface{}) *JSONQ {
	return j.Where(key, signEndsWith, val)
}

// WhereContains satisfies Where clause which contains provided value(string)
func (j *JSONQ) WhereContains(key string, val interface{}) *JSONQ {
	return j.Where(key, signContains, val)
}

// WhereStrictContains satisfies Where clause which contains provided value(string).
// This is case sensitive
func (j *JSONQ) WhereStrictContains(key string, val interface{}) *JSONQ {
	return j.Where(key, signStrictContains, val)
}

// findInArray traverses through a list and returns the value list.
// This helps to process Where/OrWhere queries
func (j *JSONQ) findInArray(aa []interface{}) []interface{} {
	result := make([]interface{}, 0)
	for _, a := range aa {
		if m, ok := a.(map[string]interface{}); ok {
			result = append(result, j.findInMap(m)...)
		}
	}
	return result
}

// findInMap traverses through a map and returns the matched value list.
// This helps to process Where/OrWhere queries
func (j *JSONQ) findInMap(vm map[string]interface{}) []interface{} {
	result := make([]interface{}, 0)
	orPassed := false
	for _, qList := range j.queries {
		andPassed := true
		for _, q := range qList {
			if mv, o := vm[q.key]; o {
				cf, ok := j.queryMap[q.operator]
				if !ok {
					j.addError(fmt.Errorf("invalid operator %s", q.operator))
					return result
				}
				qb, err := cf(mv, q.value)
				if err != nil {
					j.addError(err)
				}
				andPassed = andPassed && qb
			} else {
				andPassed = false
			}
		}
		orPassed = orPassed || andPassed
	}
	if orPassed {
		result = append(result, vm)
	}
	return result
}

// processQuery makes the result
func (j *JSONQ) processQuery() *JSONQ {
	if vm, ok := j.jsonContent.(map[string]interface{}); ok {
		j.jsonContent = j.findInMap(vm)
	}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		j.jsonContent = j.findInArray(aa)
	}
	return j
}

// prepare builds the queries
func (j *JSONQ) prepare() *JSONQ {
	if len(j.queries) > 0 {
		j.processQuery()
	}
	j.queryIndex = 0
	return j
}

// GroupBy builds a chunk of exact matched data in a group list using provided attribute/column/property
func (j *JSONQ) GroupBy(property string) *JSONQ {
	j.prepare()

	dt := map[string][]interface{}{}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, a := range aa {
			if vm, ok := a.(map[string]interface{}); ok {
				if v, o := vm[property]; o {
					if _, ok := dt[toString(v)]; ok {
						dt[toString(v)] = append(dt[toString(v)], vm)
					} else {
						dt[toString(v)] = []interface{}{vm}
					}
				}
			}
		}
	}
	// replace the new result with the previous result
	j.jsonContent = dt
	return j
}

// Sort sorts an array
// default ascending order, pass "desc" for descending order
func (j *JSONQ) Sort(order ...string) *JSONQ {
	j.prepare()

	asc := true
	if len(order) > 1 {
		return j.addError(fmt.Errorf("sort accepts only one argument asc/desc"))
	}
	if len(order) > 0 && order[0] == "desc" {
		asc = false
	}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		j.jsonContent = sortList(arr, asc)
	}
	return j
}

// SortBy sorts an array
// default ascending order, pass "desc" for descending order
func (j *JSONQ) SortBy(order ...string) *JSONQ {
	j.prepare()
	asc := true
	if len(order) == 0 {
		return j.addError(fmt.Errorf("provide at least one argument as property name"))
	}
	if len(order) > 2 {
		return j.addError(fmt.Errorf("sort accepts only two arguments. first argument property name and second argument asc/desc"))
	}

	if len(order) > 1 && order[1] == "desc" {
		asc = false
	}

	return j.sortBy(order[0], asc)
}

// sortBy sorts list of map
func (j *JSONQ) sortBy(property string, asc bool) *JSONQ {
	sortResult, ok := j.jsonContent.([]interface{})
	if !ok {
		return j
	}
	if len(sortResult) == 0 {
		return j
	}

	sm := &sortMap{}
	sm.key = property
	if !asc {
		sm.desc = true
	}
	sm.Sort(sortResult)

	// replace the new result with the previous result
	j.jsonContent = sortResult
	return j
}

// Only collects the properties from a list of object
func (j *JSONQ) Only(properties ...string) interface{} {
	j.prepare()
	result := []interface{}{}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, am := range aa {
			if mv, ok := am.(map[string]interface{}); ok {
				tmap := map[string]interface{}{}
				for _, prop := range properties {
					if v, ok := mv[prop]; ok {
						tmap[prop] = v
					}
				}
				if len(tmap) > 0 {
					result = append(result, tmap)
				}
			}
		}
	}
	return result
}

// Pluck build an array of vlaues form a property of a list of objects
func (j *JSONQ) Pluck(property string) interface{} {
	j.prepare()
	result := []interface{}{}
	if aa, ok := j.jsonContent.([]interface{}); ok {
		for _, am := range aa {
			if mv, ok := am.(map[string]interface{}); ok {
				if v, ok := mv[property]; ok {
					result = append(result, v)
				}
			}
		}
	}
	return result
}

// reset resets the current state of JSONQ instance
func (j *JSONQ) reset() *JSONQ {
	j.jsonContent = j.rootJSONContent
	j.node = ""
	j.queries = make([][]query, 0)
	j.attributes = make([]string, 0)
	j.queryIndex = 0
	return j
}

// Reset resets the current state of JSON instance and make a fresh object with the original json content
func (j *JSONQ) Reset() *JSONQ {
	return j.reset()
}

// Get return the result
func (j *JSONQ) Get() interface{} {
	if len(j.attributes) > 0 {
		return j.prepare().Only(j.attributes...)
	}
	return j.prepare().jsonContent
}

// First returns the first element of a list
func (j *JSONQ) First() interface{} {
	res := j.prepare().jsonContent
	if arr, ok := res.([]interface{}); ok {
		if len(arr) > 0 {
			return arr[0]
		}
	}
	return empty
}

// Last returns the last element of a list
func (j *JSONQ) Last() interface{} {
	res := j.prepare().jsonContent
	if arr, ok := res.([]interface{}); ok {
		if l := len(arr); l > 0 {
			return arr[l-1]
		}
	}
	return empty
}

// Nth returns the nth element of a list
func (j *JSONQ) Nth(index int) interface{} {
	if index == 0 {
		j.addError(fmt.Errorf("index is not zero based"))
		return empty
	}

	res := j.prepare().jsonContent
	if arr, ok := res.([]interface{}); ok {
		alen := len(arr)
		if alen == 0 {
			j.addError(fmt.Errorf("list is empty"))
			return empty
		}
		if abs(index) > alen {
			j.addError(fmt.Errorf("index out of range"))
			return empty
		}
		if index > 0 {
			return arr[index-1]
		}
		return arr[alen+index]
	}
	return empty
}

// Find returns the result of a exact matching path
func (j *JSONQ) Find(path string) interface{} {
	return j.From(path).Get()
}

// Count returns the number of total items.
// This could be a length of list/array/map
func (j *JSONQ) Count() int {
	j.prepare()
	lnth := 0
	// list of items
	if list, ok := j.jsonContent.([]interface{}); ok {
		lnth = len(list)
	}
	// return map len // TODO: need to think about map
	if m, ok := j.jsonContent.(map[string]interface{}); ok {
		lnth = len(m)
	}
	// group data items
	if m, ok := j.jsonContent.(map[string][]interface{}); ok {
		lnth = len(m)
	}

	return lnth
}

// getFloatValFromArray returns a list of float64 values from array/map for aggregation
func (j *JSONQ) getFloatValFromArray(arr []interface{}, property ...string) []float64 {
	ff := []float64{}
	for _, a := range arr {
		if av, ok := a.(float64); ok {
			if len(property) > 0 {
				j.addError(fmt.Errorf("unnecessary property name for array"))
				return nil
			}
			ff = append(ff, av)
		}
		if mv, ok := a.(map[string]interface{}); ok {
			if len(property) == 0 {
				j.addError(fmt.Errorf("property name can not be empty for object"))
				return nil
			}
			if fi, ok := mv[property[0]]; ok {
				if flt, ok := fi.(float64); ok {
					ff = append(ff, flt)
				} else {
					j.addError(fmt.Errorf("property %s's value '%v' is not numeric", property[0], fi))
					return nil
				}
			} else {
				j.addError(fmt.Errorf("property '%s' does not exist", property[0]))
				return nil
			}
		}
	}

	return ff
}

// getAggregationValues returns a list of float64 values for aggregation
func (j *JSONQ) getAggregationValues(property ...string) []float64 {
	j.prepare()

	ff := []float64{}
	if arr, ok := j.jsonContent.([]interface{}); ok {
		ff = j.getFloatValFromArray(arr, property...)
	}

	if mv, ok := j.jsonContent.(map[string]interface{}); ok {
		if len(property) == 0 {
			j.addError(fmt.Errorf("property can not be empty for object"))
			return nil
		}
		if fi, ok := mv[property[0]]; ok {
			if flt, ok := fi.(float64); ok {
				ff = append(ff, flt)
			} else {
				j.addError(fmt.Errorf("property %s's value '%v' is not numeric", property[0], fi))
				return nil
			}
		} else {
			j.addError(fmt.Errorf("property '%s' does not exist", property[0]))
			return nil
		}
	}
	return ff
}

// Sum returns sum of values from array or from map using property
func (j *JSONQ) Sum(property ...string) float64 {
	var sum float64
	for _, flt := range j.getAggregationValues(property...) {
		sum += flt
	}
	return sum
}

// Avg returns average of values from array or from map using property
func (j *JSONQ) Avg(property ...string) float64 {
	var sum float64
	fl := j.getAggregationValues(property...)
	for _, flt := range fl {
		sum += flt
	}
	return sum / float64(len(fl))
}

// Min returns minimum value from array or from map using property
func (j *JSONQ) Min(property ...string) float64 {
	var min float64
	flist := j.getAggregationValues(property...)
	if len(flist) > 0 {
		min = flist[0]
	}
	for _, flt := range flist {
		if flt < min {
			min = flt
		}
	}
	return min
}

// Max returns maximum value from array or from map using property
func (j *JSONQ) Max(property ...string) float64 {
	var max float64
	flist := j.getAggregationValues(property...)
	if len(flist) > 0 {
		max = flist[0]
	}
	for _, flt := range flist {
		if flt > max {
			max = flt
		}
	}
	return max
}
