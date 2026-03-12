package main

import "fmt"

// Custom type implementing the json.Marshaler interface
type Status struct {
	Code    int
	Message string
}

func (s Status) MarshalJSON() ([]byte, error) {
	return []byte(`{"code":` + fmt.Sprint(s.Code) + `,"msg":"` + s.Message + `"}`), nil
}

// Anonymous embedded struct
type Base struct {
	ID        int    `json:"id"`
	CreatedBy string `json:"created_by"`
}

// Named field struct
type Address struct {
	City   string `json:"city"`
	Street string `json:"street"`
	Detail any    `json:"detail,omitempty"` // any: triggers nested SWITCH_OPS / on-the-fly compilation
}

// Large struct; first appearance in interface{} requires on-the-fly Blueprint compilation
type GeoLocation struct {
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	Altitude  float64  `json:"altitude"`
	Accuracy  float64  `json:"accuracy"`
	Provider  string   `json:"provider"`
	Country   string   `json:"country"`
	Province  string   `json:"province"`
	District  string   `json:"district"`
	ZipCode   string   `json:"zip_code"`
	Timezone  string   `json:"timezone"`
	Timestamp int64    `json:"timestamp"`
	Tags      []string `json:"tags"`
}

type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type User struct {
	Base                       // Anonymous embedding
	Name     string            `json:"name"`
	Age      int               `json:"age"`
	Addr     Address           `json:"addr"`    // Named field struct
	WorkAt   *Address          `json:"work_at"` // Pointer to named field struct
	HomeAt   *Address          `json:"home_at"` // nil pointer
	Status   Status            `json:"status"`  // json.Marshaler in the middle position
	Tags     []Tag             `json:"tags"`    // slice of nested structs
	Meta     map[string]string `json:"meta"`    // map field
	Nickname string            `json:"nickname"`
	Extra    any               `json:"extra"` // any field
}

// NewTestUser creates a new test User instance with predefined data.
func NewTestUser() User {
	return User{
		Base:   Base{ID: 1, CreatedBy: "system"},
		Name:   "alice",
		Age:    30,
		Addr:   Address{City: "Beijing", Street: "Chang'an Ave"},
		WorkAt: &Address{City: "Shanghai", Street: "Nanjing Rd"},
		// HomeAt: nil
		Status: Status{Code: 200, Message: "active"},
		Tags: []Tag{
			{Key: "role", Value: "admin"},
			{Key: "dept", Value: "eng"},
			{Key: "level", Value: "senior"},
		},
		Meta: map[string]string{
			"source": "api",
			"region": "cn-east",
			"aa":     "cn-east",
			"bb":     "cn-east",
			"cc":     "cn-east",
		},
		Nickname: "lambit",
		Extra: Address{
			City:   "Shenzhen",
			Street: "Keyuan Rd",
			Detail: GeoLocation{
				Lat: 22.5431, Lng: 114.0579, Altitude: 15.3, Accuracy: 10.0,
				Provider: "gps", Country: "China", Province: "Guangdong",
				District: "Nanshan", ZipCode: "518057", Timezone: "Asia/Shanghai",
				Timestamp: 1709472000,
				Tags:      []string{"office", "primary", "verified"},
			},
		},
	}
}
