/*
 * Copyright 2021 ByteDance Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package twitter

type TwitterStruct struct {
	Statuses       []Statuses     `json:"statuses"`
	SearchMetadata SearchMetadata `json:"search_metadata"`
}

type Hashtags struct {
	Text    string `json:"text"`
	Indices []int  `json:"indices"`
}

type Entities struct {
	Urls         []interface{} `json:"urls"`
	Hashtags     []Hashtags    `json:"hashtags"`
	UserMentions []interface{} `json:"user_mentions"`
}

type Metadata struct {
	IsoLanguageCode string `json:"iso_language_code"`
	ResultType      string `json:"result_type"`
}

type Urls struct {
	ExpandedURL interface{} `json:"expanded_url"`
	URL         string      `json:"url"`
	Indices     []int       `json:"indices"`
}

type URL struct {
	Urls []Urls `json:"urls"`
}

type Description struct {
	Urls []interface{} `json:"urls"`
}

type UserEntities struct {
	URL         URL         `json:"url"`
	Description Description `json:"description"`
}

type User struct {
	ProfileSidebarFillColor        string       `json:"profile_sidebar_fill_color"`
	ProfileSidebarBorderColor      string       `json:"profile_sidebar_border_color"`
	ProfileBackgroundTile          bool         `json:"profile_background_tile"`
	Name                           string       `json:"name"`
	ProfileImageURL                string       `json:"profile_image_url"`
	CreatedAt                      string       `json:"created_at"`
	Location                       string       `json:"location"`
	FollowRequestSent              interface{}  `json:"follow_request_sent"`
	ProfileLinkColor               string       `json:"profile_link_color"`
	IsTranslator                   bool         `json:"is_translator"`
	IDStr                          string       `json:"id_str"`
	Entities                       UserEntities `json:"entities"`
	DefaultProfile                 bool         `json:"default_profile"`
	ContributorsEnabled            bool         `json:"contributors_enabled"`
	FavouritesCount                int          `json:"favourites_count"`
	URL                            interface{}  `json:"url"`
	ProfileImageURLHTTPS           string       `json:"profile_image_url_https"`
	UtcOffset                      int          `json:"utc_offset"`
	ID                             int          `json:"id"`
	ProfileUseBackgroundImage      bool         `json:"profile_use_background_image"`
	ListedCount                    int          `json:"listed_count"`
	ProfileTextColor               string       `json:"profile_text_color"`
	Lang                           string       `json:"lang"`
	FollowersCount                 int          `json:"followers_count"`
	Protected                      bool         `json:"protected"`
	Notifications                  interface{}  `json:"notifications"`
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
	Following                      interface{}  `json:"following"`
	ShowAllInlineMedia             bool         `json:"show_all_inline_media"`
	ScreenName                     string       `json:"screen_name"`
}

type Statuses struct {
	Coordinates          interface{} `json:"coordinates"`
	Favorited            bool        `json:"favorited"`
	Truncated            bool        `json:"truncated"`
	CreatedAt            string      `json:"created_at"`
	IDStr                string      `json:"id_str"`
	Entities             Entities    `json:"entities"`
	InReplyToUserIDStr   interface{} `json:"in_reply_to_user_id_str"`
	Contributors         interface{} `json:"contributors"`
	Text                 string      `json:"text"`
	Metadata             Metadata    `json:"metadata"`
	RetweetCount         int         `json:"retweet_count"`
	InReplyToStatusIDStr interface{} `json:"in_reply_to_status_id_str"`
	ID                   int64       `json:"id"`
	Geo                  interface{} `json:"geo"`
	Retweeted            bool        `json:"retweeted"`
	InReplyToUserID      interface{} `json:"in_reply_to_user_id"`
	Place                interface{} `json:"place"`
	User                 User        `json:"user"`
	InReplyToScreenName  interface{} `json:"in_reply_to_screen_name"`
	Source               string      `json:"source"`
	InReplyToStatusID    interface{} `json:"in_reply_to_status_id"`
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
