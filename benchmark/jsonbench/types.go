// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonbench

import (
	"time"
)

type (
	CanadaRoot struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string `json:"type"`
			Properties struct {
				Name string `json:"name"`
			} `json:"properties"`
			Geometry struct {
				Type        string         `json:"type"`
				Coordinates [][][2]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
)

type (
	CITMRoot struct {
		AreaNames                map[int64]string `json:"areaNames"`
		AudienceSubCategoryNames map[int64]string `json:"audienceSubCategoryNames"`
		BlockNames               map[int64]string `json:"blockNames"`
		Events                   map[int64]struct {
			Description string `json:"description"`
			ID          int    `json:"id"`
			Logo        string `json:"logo"`
			Name        string `json:"name"`
			SubTopicIds []int  `json:"subTopicIds"`
			SubjectCode any    `json:"subjectCode"`
			Subtitle    any    `json:"subtitle"`
			TopicIds    []int  `json:"topicIds"`
		} `json:"events"`
		Performances []struct {
			EventID int `json:"eventId"`
			ID      int `json:"id"`
			Logo    any `json:"logo"`
			Name    any `json:"name"`
			Prices  []struct {
				Amount                int   `json:"amount"`
				AudienceSubCategoryID int64 `json:"audienceSubCategoryId"`
				SeatCategoryID        int64 `json:"seatCategoryId"`
			} `json:"prices"`
			SeatCategories []struct {
				Areas []struct {
					AreaID   int   `json:"areaId"`
					BlockIds []any `json:"blockIds"`
				} `json:"areas"`
				SeatCategoryID int `json:"seatCategoryId"`
			} `json:"seatCategories"`
			SeatMapImage any    `json:"seatMapImage"`
			Start        int64  `json:"start"`
			VenueCode    string `json:"venueCode"`
		} `json:"performances"`
		SeatCategoryNames map[uint64]string   `json:"seatCategoryNames"`
		SubTopicNames     map[uint64]string   `json:"subTopicNames"`
		SubjectNames      map[uint64]string   `json:"subjectNames"`
		TopicNames        map[uint64]string   `json:"topicNames"`
		TopicSubTopics    map[uint64][]uint64 `json:"topicSubTopics"`
		VenueNames        map[string]string   `json:"venueNames"`
	}
)

type (
	GolangRoot struct {
		Tree     *GolangNode `json:"tree"`
		Username string      `json:"username"`
	}
	GolangNode struct {
		Name     string       `json:"name"`
		Kids     []GolangNode `json:"kids"`
		CLWeight float64      `json:"cl_weight"`
		Touches  int          `json:"touches"`
		MinT     uint64       `json:"min_t"`
		MaxT     uint64       `json:"max_t"`
		MeanT    uint64       `json:"mean_t"`
	}
)

type (
	StringRoot struct {
		Arabic                             string `json:"Arabic"`
		ArabicPresentationFormsA           string `json:"Arabic Presentation Forms-A"`
		ArabicPresentationFormsB           string `json:"Arabic Presentation Forms-B"`
		Armenian                           string `json:"Armenian"`
		Arrows                             string `json:"Arrows"`
		Bengali                            string `json:"Bengali"`
		Bopomofo                           string `json:"Bopomofo"`
		BoxDrawing                         string `json:"Box Drawing"`
		CJKCompatibility                   string `json:"CJK Compatibility"`
		CJKCompatibilityForms              string `json:"CJK Compatibility Forms"`
		CJKCompatibilityIdeographs         string `json:"CJK Compatibility Ideographs"`
		CJKSymbolsAndPunctuation           string `json:"CJK Symbols and Punctuation"`
		CJKUnifiedIdeographs               string `json:"CJK Unified Ideographs"`
		CJKUnifiedIdeographsExtensionA     string `json:"CJK Unified Ideographs Extension A"`
		CJKUnifiedIdeographsExtensionB     string `json:"CJK Unified Ideographs Extension B"`
		Cherokee                           string `json:"Cherokee"`
		CurrencySymbols                    string `json:"Currency Symbols"`
		Cyrillic                           string `json:"Cyrillic"`
		CyrillicSupplementary              string `json:"Cyrillic Supplementary"`
		Devanagari                         string `json:"Devanagari"`
		EnclosedAlphanumerics              string `json:"Enclosed Alphanumerics"`
		EnclosedCJKLettersAndMonths        string `json:"Enclosed CJK Letters and Months"`
		Ethiopic                           string `json:"Ethiopic"`
		GeometricShapes                    string `json:"Geometric Shapes"`
		Georgian                           string `json:"Georgian"`
		GreekAndCoptic                     string `json:"Greek and Coptic"`
		Gujarati                           string `json:"Gujarati"`
		Gurmukhi                           string `json:"Gurmukhi"`
		HangulCompatibilityJamo            string `json:"Hangul Compatibility Jamo"`
		HangulJamo                         string `json:"Hangul Jamo"`
		HangulSyllables                    string `json:"Hangul Syllables"`
		Hebrew                             string `json:"Hebrew"`
		Hiragana                           string `json:"Hiragana"`
		IPAExtentions                      string `json:"IPA Extentions"`
		KangxiRadicals                     string `json:"Kangxi Radicals"`
		Katakana                           string `json:"Katakana"`
		Khmer                              string `json:"Khmer"`
		KhmerSymbols                       string `json:"Khmer Symbols"`
		Latin                              string `json:"Latin"`
		LatinExtendedAdditional            string `json:"Latin Extended Additional"`
		Latin1Supplement                   string `json:"Latin-1 Supplement"`
		LatinExtendedA                     string `json:"Latin-Extended A"`
		LatinExtendedB                     string `json:"Latin-Extended B"`
		LetterlikeSymbols                  string `json:"Letterlike Symbols"`
		Malayalam                          string `json:"Malayalam"`
		MathematicalAlphanumericSymbols    string `json:"Mathematical Alphanumeric Symbols"`
		MathematicalOperators              string `json:"Mathematical Operators"`
		MiscellaneousSymbols               string `json:"Miscellaneous Symbols"`
		Mongolian                          string `json:"Mongolian"`
		NumberForms                        string `json:"Number Forms"`
		Oriya                              string `json:"Oriya"`
		PhoneticExtensions                 string `json:"Phonetic Extensions"`
		SupplementalArrowsB                string `json:"Supplemental Arrows-B"`
		Syriac                             string `json:"Syriac"`
		Tamil                              string `json:"Tamil"`
		Thaana                             string `json:"Thaana"`
		Thai                               string `json:"Thai"`
		UnifiedCanadianAboriginalSyllabics string `json:"Unified Canadian Aboriginal Syllabics"`
		YiRadicals                         string `json:"Yi Radicals"`
		YiSyllables                        string `json:"Yi Syllables"`
	}
)

type (
	SyntheaRoot struct {
		Entry []struct {
			FullURL string `json:"fullUrl"`
			Request *struct {
				Method string `json:"method"`
				URL    string `json:"url"`
			} `json:"request"`
			Resource *struct {
				AbatementDateTime time.Time   `json:"abatementDateTime"`
				AchievementStatus SyntheaCode `json:"achievementStatus"`
				Active            bool        `json:"active"`
				Activity          []struct {
					Detail *struct {
						Code     SyntheaCode      `json:"code"`
						Location SyntheaReference `json:"location"`
						Status   string           `json:"status"`
					} `json:"detail"`
				} `json:"activity"`
				Address        []SyntheaAddress   `json:"address"`
				Addresses      []SyntheaReference `json:"addresses"`
				AuthoredOn     time.Time          `json:"authoredOn"`
				BillablePeriod SyntheaRange       `json:"billablePeriod"`
				BirthDate      string             `json:"birthDate"`
				CareTeam       []struct {
					Provider  SyntheaReference `json:"provider"`
					Reference string           `json:"reference"`
					Role      SyntheaCode      `json:"role"`
					Sequence  int64            `json:"sequence"`
				} `json:"careTeam"`
				Category       []SyntheaCode    `json:"category"`
				Claim          SyntheaReference `json:"claim"`
				Class          SyntheaCoding    `json:"class"`
				ClinicalStatus SyntheaCode      `json:"clinicalStatus"`
				Code           SyntheaCode      `json:"code"`
				Communication  []struct {
					Language SyntheaCode `json:"language"`
				} `json:"communication"`
				Component []struct {
					Code          SyntheaCode   `json:"code"`
					ValueQuantity SyntheaCoding `json:"valueQuantity"`
				} `json:"component"`
				Contained []struct {
					Beneficiary  SyntheaReference   `json:"beneficiary"`
					ID           string             `json:"id"`
					Intent       string             `json:"intent"`
					Payor        []SyntheaReference `json:"payor"`
					Performer    []SyntheaReference `json:"performer"`
					Requester    SyntheaReference   `json:"requester"`
					ResourceType string             `json:"resourceType"`
					Status       string             `json:"status"`
					Subject      SyntheaReference   `json:"subject"`
					Type         SyntheaCode        `json:"type"`
				} `json:"contained"`
				Created          time.Time   `json:"created"`
				DeceasedDateTime time.Time   `json:"deceasedDateTime"`
				Description      SyntheaCode `json:"description"`
				Diagnosis        []struct {
					DiagnosisReference SyntheaReference `json:"diagnosisReference"`
					Sequence           int64            `json:"sequence"`
					Type               []SyntheaCode    `json:"type"`
				} `json:"diagnosis"`
				DosageInstruction []struct {
					AsNeededBoolean bool `json:"asNeededBoolean"`
					DoseAndRate     []struct {
						DoseQuantity *struct {
							Value float64 `json:"value"`
						} `json:"doseQuantity"`
						Type SyntheaCode `json:"type"`
					} `json:"doseAndRate"`
					Sequence int64 `json:"sequence"`
					Timing   *struct {
						Repeat *struct {
							Frequency  int64   `json:"frequency"`
							Period     float64 `json:"period"`
							PeriodUnit string  `json:"periodUnit"`
						} `json:"repeat"`
					} `json:"timing"`
				} `json:"dosageInstruction"`
				EffectiveDateTime time.Time          `json:"effectiveDateTime"`
				Encounter         SyntheaReference   `json:"encounter"`
				Extension         []SyntheaExtension `json:"extension"`
				Gender            string             `json:"gender"`
				Goal              []SyntheaReference `json:"goal"`
				ID                string             `json:"id"`
				Identifier        []struct {
					System string      `json:"system"`
					Type   SyntheaCode `json:"type"`
					Use    string      `json:"use"`
					Value  string      `json:"value"`
				} `json:"identifier"`
				Insurance []struct {
					Coverage SyntheaReference `json:"coverage"`
					Focal    bool             `json:"focal"`
					Sequence int64            `json:"sequence"`
				} `json:"insurance"`
				Insurer SyntheaReference `json:"insurer"`
				Intent  string           `json:"intent"`
				Issued  time.Time        `json:"issued"`
				Item    []struct {
					Adjudication []struct {
						Amount   SyntheaCurrency `json:"amount"`
						Category SyntheaCode     `json:"category"`
					} `json:"adjudication"`
					Category                SyntheaCode        `json:"category"`
					DiagnosisSequence       []int64            `json:"diagnosisSequence"`
					Encounter               []SyntheaReference `json:"encounter"`
					InformationSequence     []int64            `json:"informationSequence"`
					LocationCodeableConcept SyntheaCode        `json:"locationCodeableConcept"`
					Net                     SyntheaCurrency    `json:"net"`
					ProcedureSequence       []int64            `json:"procedureSequence"`
					ProductOrService        SyntheaCode        `json:"productOrService"`
					Sequence                int64              `json:"sequence"`
					ServicedPeriod          SyntheaRange       `json:"servicedPeriod"`
				} `json:"item"`
				LifecycleStatus           string             `json:"lifecycleStatus"`
				ManagingOrganization      []SyntheaReference `json:"managingOrganization"`
				MaritalStatus             SyntheaCode        `json:"maritalStatus"`
				MedicationCodeableConcept SyntheaCode        `json:"medicationCodeableConcept"`
				MultipleBirthBoolean      bool               `json:"multipleBirthBoolean"`
				Name                      any                `json:"name"`
				NumberOfInstances         int64              `json:"numberOfInstances"`
				NumberOfSeries            int64              `json:"numberOfSeries"`
				OccurrenceDateTime        time.Time          `json:"occurrenceDateTime"`
				OnsetDateTime             time.Time          `json:"onsetDateTime"`
				Outcome                   string             `json:"outcome"`
				Participant               []struct {
					Individual SyntheaReference `json:"individual"`
					Member     SyntheaReference `json:"member"`
					Role       []SyntheaCode    `json:"role"`
				} `json:"participant"`
				Patient SyntheaReference `json:"patient"`
				Payment *struct {
					Amount SyntheaCurrency `json:"amount"`
				} `json:"payment"`
				PerformedPeriod SyntheaRange     `json:"performedPeriod"`
				Period          SyntheaRange     `json:"period"`
				Prescription    SyntheaReference `json:"prescription"`
				PrimarySource   bool             `json:"primarySource"`
				Priority        SyntheaCode      `json:"priority"`
				Procedure       []struct {
					ProcedureReference SyntheaReference `json:"procedureReference"`
					Sequence           int64            `json:"sequence"`
				} `json:"procedure"`
				Provider        SyntheaReference   `json:"provider"`
				ReasonCode      []SyntheaCode      `json:"reasonCode"`
				ReasonReference []SyntheaReference `json:"reasonReference"`
				RecordedDate    time.Time          `json:"recordedDate"`
				Referral        SyntheaReference   `json:"referral"`
				Requester       SyntheaReference   `json:"requester"`
				ResourceType    string             `json:"resourceType"`
				Result          []SyntheaReference `json:"result"`
				Series          []struct {
					BodySite SyntheaCoding `json:"bodySite"`
					Instance []struct {
						Number   int64         `json:"number"`
						SopClass SyntheaCoding `json:"sopClass"`
						Title    string        `json:"title"`
						UID      string        `json:"uid"`
					} `json:"instance"`
					Modality          SyntheaCoding `json:"modality"`
					Number            int64         `json:"number"`
					NumberOfInstances int64         `json:"numberOfInstances"`
					Started           string        `json:"started"`
					UID               string        `json:"uid"`
				} `json:"series"`
				ServiceProvider SyntheaReference `json:"serviceProvider"`
				Started         time.Time        `json:"started"`
				Status          string           `json:"status"`
				Subject         SyntheaReference `json:"subject"`
				SupportingInfo  []struct {
					Category       SyntheaCode      `json:"category"`
					Sequence       int64            `json:"sequence"`
					ValueReference SyntheaReference `json:"valueReference"`
				} `json:"supportingInfo"`
				Telecom              []map[string]string `json:"telecom"`
				Text                 map[string]string   `json:"text"`
				Total                any                 `json:"total"`
				Type                 any                 `json:"type"`
				Use                  string              `json:"use"`
				VaccineCode          SyntheaCode         `json:"vaccineCode"`
				ValueCodeableConcept SyntheaCode         `json:"valueCodeableConcept"`
				ValueQuantity        SyntheaCoding       `json:"valueQuantity"`
				VerificationStatus   SyntheaCode         `json:"verificationStatus"`
			} `json:"resource"`
		} `json:"entry"`
		ResourceType string `json:"resourceType"`
		Type         string `json:"type"`
	}
	SyntheaCode struct {
		Coding []SyntheaCoding `json:"coding"`
		Text   string          `json:"text"`
	}
	SyntheaCoding struct {
		Code    string  `json:"code"`
		Display string  `json:"display"`
		System  string  `json:"system"`
		Unit    string  `json:"unit"`
		Value   float64 `json:"value"`
	}
	SyntheaReference struct {
		Display   string `json:"display"`
		Reference string `json:"reference"`
	}
	SyntheaAddress struct {
		City       string             `json:"city"`
		Country    string             `json:"country"`
		Extension  []SyntheaExtension `json:"extension"`
		Line       []string           `json:"line"`
		PostalCode string             `json:"postalCode"`
		State      string             `json:"state"`
	}
	SyntheaExtension struct {
		URL          string             `json:"url"`
		ValueAddress SyntheaAddress     `json:"valueAddress"`
		ValueCode    string             `json:"valueCode"`
		ValueDecimal float64            `json:"valueDecimal"`
		ValueString  string             `json:"valueString"`
		Extension    []SyntheaExtension `json:"extension"`
	}
	SyntheaRange struct {
		End   time.Time `json:"end"`
		Start time.Time `json:"start"`
	}
	SyntheaCurrency struct {
		Currency string  `json:"currency"`
		Value    float64 `json:"value"`
	}
)

type (
	TwitterRoot struct {
		Statuses       []TwitterStatus `json:"statuses"`
		SearchMetadata struct {
			CompletedIn float64 `json:"completed_in"`
			MaxID       int64   `json:"max_id"`
			MaxIDStr    int64   `json:"max_id_str,string"`
			NextResults string  `json:"next_results"`
			Query       string  `json:"query"`
			RefreshURL  string  `json:"refresh_url"`
			Count       int     `json:"count"`
			SinceID     int     `json:"since_id"`
			SinceIDStr  int     `json:"since_id_str,string"`
		} `json:"search_metadata"`
	}
	TwitterStatus struct {
		Metadata struct {
			ResultType      string `json:"result_type"`
			IsoLanguageCode string `json:"iso_language_code"`
		} `json:"metadata"`
		CreatedAt            string          `json:"created_at"`
		ID                   int64           `json:"id"`
		IDStr                int64           `json:"id_str,string"`
		Text                 string          `json:"text"`
		Source               string          `json:"source"`
		Truncated            bool            `json:"truncated"`
		InReplyToStatusID    int64           `json:"in_reply_to_status_id"`
		InReplyToStatusIDStr int64           `json:"in_reply_to_status_id_str,string"`
		InReplyToUserID      int64           `json:"in_reply_to_user_id"`
		InReplyToUserIDStr   int64           `json:"in_reply_to_user_id_str,string"`
		InReplyToScreenName  string          `json:"in_reply_to_screen_name"`
		User                 TwitterUser     `json:"user,omitempty"`
		Geo                  any             `json:"geo"`
		Coordinates          any             `json:"coordinates"`
		Place                any             `json:"place"`
		Contributors         any             `json:"contributors"`
		RetweetedStatus      *TwitterStatus  `json:"retweeted_status"`
		RetweetCount         int             `json:"retweet_count"`
		FavoriteCount        int             `json:"favorite_count"`
		Entities             TwitterEntities `json:"entities,omitempty"`
		Favorited            bool            `json:"favorited"`
		Retweeted            bool            `json:"retweeted"`
		PossiblySensitive    bool            `json:"possibly_sensitive"`
		Lang                 string          `json:"lang"`
	}
	TwitterUser struct {
		ID                             int64           `json:"id"`
		IDStr                          string          `json:"id_str"`
		Name                           string          `json:"name"`
		ScreenName                     string          `json:"screen_name"`
		Location                       string          `json:"location"`
		Description                    string          `json:"description"`
		URL                            any             `json:"url"`
		Entities                       TwitterEntities `json:"entities"`
		Protected                      bool            `json:"protected"`
		FollowersCount                 int             `json:"followers_count"`
		FriendsCount                   int             `json:"friends_count"`
		ListedCount                    int             `json:"listed_count"`
		CreatedAt                      string          `json:"created_at"`
		FavouritesCount                int             `json:"favourites_count"`
		UtcOffset                      int             `json:"utc_offset"`
		TimeZone                       string          `json:"time_zone"`
		GeoEnabled                     bool            `json:"geo_enabled"`
		Verified                       bool            `json:"verified"`
		StatusesCount                  int             `json:"statuses_count"`
		Lang                           string          `json:"lang"`
		ContributorsEnabled            bool            `json:"contributors_enabled"`
		IsTranslator                   bool            `json:"is_translator"`
		IsTranslationEnabled           bool            `json:"is_translation_enabled"`
		ProfileBackgroundColor         string          `json:"profile_background_color"`
		ProfileBackgroundImageURL      string          `json:"profile_background_image_url"`
		ProfileBackgroundImageURLHTTPS string          `json:"profile_background_image_url_https"`
		ProfileBackgroundTile          bool            `json:"profile_background_tile"`
		ProfileImageURL                string          `json:"profile_image_url"`
		ProfileImageURLHTTPS           string          `json:"profile_image_url_https"`
		ProfileBannerURL               string          `json:"profile_banner_url"`
		ProfileLinkColor               string          `json:"profile_link_color"`
		ProfileSidebarBorderColor      string          `json:"profile_sidebar_border_color"`
		ProfileSidebarFillColor        string          `json:"profile_sidebar_fill_color"`
		ProfileTextColor               string          `json:"profile_text_color"`
		ProfileUseBackgroundImage      bool            `json:"profile_use_background_image"`
		DefaultProfile                 bool            `json:"default_profile"`
		DefaultProfileImage            bool            `json:"default_profile_image"`
		Following                      bool            `json:"following"`
		FollowRequestSent              bool            `json:"follow_request_sent"`
		Notifications                  bool            `json:"notifications"`
	}
	TwitterEntities struct {
		Hashtags     []any        `json:"hashtags"`
		Symbols      []any        `json:"symbols"`
		URL          *TwitterURL  `json:"url"`
		URLs         []TwitterURL `json:"urls"`
		UserMentions []struct {
			ScreenName string `json:"screen_name"`
			Name       string `json:"name"`
			ID         int64  `json:"id"`
			IDStr      int64  `json:"id_str,string"`
			Indices    []int  `json:"indices"`
		} `json:"user_mentions"`
		Description struct {
			URLs []TwitterURL `json:"urls"`
		} `json:"description"`
		Media []struct {
			ID            int64  `json:"id"`
			IDStr         string `json:"id_str"`
			Indices       []int  `json:"indices"`
			MediaURL      string `json:"media_url"`
			MediaURLHTTPS string `json:"media_url_https"`
			URL           string `json:"url"`
			DisplayURL    string `json:"display_url"`
			ExpandedURL   string `json:"expanded_url"`
			Type          string `json:"type"`
			Sizes         map[string]struct {
				W      int    `json:"w"`
				H      int    `json:"h"`
				Resize string `json:"resize"`
			} `json:"sizes"`
			SourceStatusID    int64 `json:"source_status_id"`
			SourceStatusIDStr int64 `json:"source_status_id_str,string"`
		} `json:"media"`
	}
	TwitterURL struct {
		URL         string       `json:"url"`
		URLs        []TwitterURL `json:"urls"`
		ExpandedURL string       `json:"expanded_url"`
		DisplayURL  string       `json:"display_url"`
		Indices     []int        `json:"indices"`
	}
)
