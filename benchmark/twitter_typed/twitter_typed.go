// Package twitter_typed provides fully-typed Twitter struct definitions
// (no interface{} fields) for benchmarking the C VM without yield overhead.
package twitter_typed

// TwitterStruct is the typed version of twitter.TwitterStruct.
// All interface{} fields are replaced with their actual concrete types
// based on the twitter.json test data.
type TwitterStruct struct {
	Statuses       []Statuses     `json:"statuses"`
	SearchMetadata SearchMetadata `json:"search_metadata"`
}

type Hashtags struct {
	Text    string `json:"text"`
	Indices []int  `json:"indices"`
}

// EntityUrl replaces the map[string]any elements in Entities.Urls / Description.Urls.
// Actual structure: {"display_url": str, "expanded_url": str, "indices": [int], "url": str}
type EntityUrl struct {
	DisplayURL  string `json:"display_url"`
	ExpandedURL string `json:"expanded_url"`
	Indices     []int  `json:"indices"`
	URL         string `json:"url"`
}

// UserMention replaces the map[string]any elements in Entities.UserMentions.
// Actual structure: {"id": int, "id_str": str, "indices": [int], "name": str, "screen_name": str}
type UserMention struct {
	ID         int    `json:"id"`
	IDStr      string `json:"id_str"`
	Indices    []int  `json:"indices"`
	Name       string `json:"name"`
	ScreenName string `json:"screen_name"`
}

// Entities: []interface{} replaced with typed slices.
type Entities struct {
	Urls         []EntityUrl   `json:"urls"`
	Hashtags     []Hashtags    `json:"hashtags"`
	UserMentions []UserMention `json:"user_mentions"`
}

type Metadata struct {
	IsoLanguageCode string `json:"iso_language_code"`
	ResultType      string `json:"result_type"`
}

// Urls: ExpandedURL was interface{}, actual type is always string.
type Urls struct {
	ExpandedURL string `json:"expanded_url"`
	URL         string `json:"url"`
	Indices     []int  `json:"indices"`
}

type URL struct {
	Urls []Urls `json:"urls"`
}

// Description: []interface{} replaced with []EntityUrl.
type Description struct {
	Urls []EntityUrl `json:"urls"`
}

type UserEntities struct {
	URL         URL         `json:"url"`
	Description Description `json:"description"`
}

// User: all interface{} fields replaced with concrete types.
// follow_request_sent: always bool
// url: string | null → *string
// notifications: always bool
// following: always bool
type User struct {
	ProfileSidebarFillColor        string       `json:"profile_sidebar_fill_color"`
	ProfileSidebarBorderColor      string       `json:"profile_sidebar_border_color"`
	ProfileBackgroundTile          bool         `json:"profile_background_tile"`
	Name                           string       `json:"name"`
	ProfileImageURL                string       `json:"profile_image_url"`
	CreatedAt                      string       `json:"created_at"`
	Location                       string       `json:"location"`
	FollowRequestSent              bool         `json:"follow_request_sent"`
	ProfileLinkColor               string       `json:"profile_link_color"`
	IsTranslator                   bool         `json:"is_translator"`
	IDStr                          string       `json:"id_str"`
	Entities                       UserEntities `json:"entities"`
	DefaultProfile                 bool         `json:"default_profile"`
	ContributorsEnabled            bool         `json:"contributors_enabled"`
	FavouritesCount                int          `json:"favourites_count"`
	URL                            *string      `json:"url"`
	ProfileImageURLHTTPS           string       `json:"profile_image_url_https"`
	UtcOffset                      int          `json:"utc_offset"`
	ID                             int          `json:"id"`
	ProfileUseBackgroundImage      bool         `json:"profile_use_background_image"`
	ListedCount                    int          `json:"listed_count"`
	ProfileTextColor               string       `json:"profile_text_color"`
	Lang                           string       `json:"lang"`
	FollowersCount                 int          `json:"followers_count"`
	Protected                      bool         `json:"protected"`
	Notifications                  bool         `json:"notifications"`
	ProfileBackgroundImageURLHTTPS string       `json:"profile_background_image_url_https"`
	ProfileBackgroundColor         string       `json:"profile_background_color"`
	Verified                       bool         `json:"verified"`
	GeoEnabled                     bool         `json:"geo_enabled"`
	TimeZone                       string       `json:"time_zone"`
	Description                    string       `json:"description"`
	DefaultProfileImage            bool         `json:"default_profile_image"`
	ProfileBackgroundImageURL      string       `json:"profile_background_image_url"`
	StatusesCount                  int          `json:"statuses_count"`
	FriendsCount                   int          `json:"friends_count"`
	Following                      bool         `json:"following"`
	ShowAllInlineMedia             bool         `json:"show_all_inline_media"`
	ScreenName                     string       `json:"screen_name"`
}

// Statuses: all interface{} fields replaced with concrete types.
// coordinates: always null → *string (will be nil)
// in_reply_to_user_id_str: string | null → *string
// contributors: always null → *string
// in_reply_to_status_id_str: string | null → *string
// geo: always null → *string
// in_reply_to_user_id: int | null → *int
// place: always null → *string
// in_reply_to_screen_name: string | null → *string
// in_reply_to_status_id: int | null → *int
type Statuses struct {
	Coordinates          *string  `json:"coordinates"`
	Favorited            bool     `json:"favorited"`
	Truncated            bool     `json:"truncated"`
	CreatedAt            string   `json:"created_at"`
	IDStr                string   `json:"id_str"`
	Entities             Entities `json:"entities"`
	InReplyToUserIDStr   *string  `json:"in_reply_to_user_id_str"`
	Contributors         *string  `json:"contributors"`
	Text                 string   `json:"text"`
	Metadata             Metadata `json:"metadata"`
	RetweetCount         int      `json:"retweet_count"`
	InReplyToStatusIDStr *string  `json:"in_reply_to_status_id_str"`
	ID                   int64    `json:"id"`
	Geo                  *string  `json:"geo"`
	Retweeted            bool     `json:"retweeted"`
	InReplyToUserID      *int     `json:"in_reply_to_user_id"`
	Place                *string  `json:"place"`
	User                 User     `json:"user"`
	InReplyToScreenName  *string  `json:"in_reply_to_screen_name"`
	Source               string   `json:"source"`
	InReplyToStatusID    *int     `json:"in_reply_to_status_id"`
}

type SearchMetadata struct {
	MaxID       int64   `json:"max_id"`
	SinceID     int64   `json:"since_id"`
	RefreshURL  string  `json:"refresh_url"`
	NextResults string  `json:"next_results"`
	Count       int     `json:"count"`
	CompletedIn float64 `json:"completed_in"`
	SinceIDStr  string  `json:"since_id_str"`
	Query       string  `json:"query"`
	MaxIDStr    string  `json:"max_id_str"`
}
