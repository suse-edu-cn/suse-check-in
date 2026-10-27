// Package locations contains the campus check-in points extracted from
// SUSE-OAA-APP: shared/.../domain/model/checkin/CheckinModels.kt (CheckinLocations).
package locations

import (
	"math/rand/v2"
	"strings"
)

const DefaultCampus = "宜宾"

type Location struct {
	Address      string  `json:"address"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	LocationJSON string  `json:"locationJson"`
}

type Campus struct {
	Name      string     `json:"name"`
	Locations []Location `json:"locations"`
}

type Summary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

var campuses = []Campus{
	{
		Name: "宜宾",
		Locations: []Location{
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804867, Longitude: 104.674665, LocationJSON: `{"point": [104.674665, 28.804867], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804868, Longitude: 104.674634, LocationJSON: `{"point": [104.674634, 28.804868], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804866, Longitude: 104.674666, LocationJSON: `{"point": [104.674666, 28.804866], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804956, Longitude: 104.674655, LocationJSON: `{"point": [104.674655, 28.804956], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805467, Longitude: 104.675577, LocationJSON: `{"point": [104.675577, 28.805467], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道干锅王(龙观嘉园店)四川轻化工大学宜宾校区", Latitude: 28.804571, Longitude: 104.666635, LocationJSON: `{"point": [104.666635, 28.804571], "address": "四川省宜宾市翠屏区白沙湾街道干锅王(龙观嘉园店)四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道品正食府四川轻化工大学宜宾校区", Latitude: 28.803802, Longitude: 104.673723, LocationJSON: `{"point": [104.673723, 28.803802], "address": "四川省宜宾市翠屏区白沙湾街道品正食府四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805614, Longitude: 104.675645, LocationJSON: `{"point": [104.675645, 28.805614], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805382, Longitude: 104.675484, LocationJSON: `{"point": [104.675484, 28.805382], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805554, Longitude: 104.675648, LocationJSON: `{"point": [104.675648, 28.805554], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805411, Longitude: 104.675551, LocationJSON: `{"point": [104.675551, 28.805411], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805473, Longitude: 104.675541, LocationJSON: `{"point": [104.675541, 28.805473], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805505, Longitude: 104.675521, LocationJSON: `{"point": [104.675521, 28.805505], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804864, Longitude: 104.674574, LocationJSON: `{"point": [104.674574, 28.804864], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804882, Longitude: 104.674446, LocationJSON: `{"point": [104.674446, 28.804882], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804857, Longitude: 104.674413, LocationJSON: `{"point": [104.674413, 28.804857], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区A4教学楼", Latitude: 28.803896, Longitude: 104.67306, LocationJSON: `{"point": [104.67306, 28.803896], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区A4教学楼"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804946, Longitude: 104.674648, LocationJSON: `{"point": [104.674648, 28.804946], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区自动化与信息工程学院", Latitude: 28.804048, Longitude: 104.670291, LocationJSON: `{"point": [104.670291, 28.804048], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区自动化与信息工程学院"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道白塔路科教·公元π", Latitude: 28.800673, Longitude: 104.674717, LocationJSON: `{"point": [104.674717, 28.800673], "address": "四川省宜宾市翠屏区白沙湾街道白塔路科教·公元π"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804943, Longitude: 104.674643, LocationJSON: `{"point": [104.674643, 28.804943], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道芭茅田四川轻化工大学宜宾校区", Latitude: 28.80777, Longitude: 104.666082, LocationJSON: `{"point": [104.666082, 28.80777], "address": "四川省宜宾市翠屏区白沙湾街道芭茅田四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道芭茅田四川轻化工大学宜宾校区", Latitude: 28.807639, Longitude: 104.666031, LocationJSON: `{"point": [104.666031, 28.807639], "address": "四川省宜宾市翠屏区白沙湾街道芭茅田四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805407, Longitude: 104.67561, LocationJSON: `{"point": [104.67561, 28.805407], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805431, Longitude: 104.675451, LocationJSON: `{"point": [104.675451, 28.805431], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805461, Longitude: 104.675512, LocationJSON: `{"point": [104.675512, 28.805461], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805402, Longitude: 104.675573, LocationJSON: `{"point": [104.675573, 28.805402], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区B6宿舍楼", Latitude: 28.806461, Longitude: 104.666897, LocationJSON: `{"point": [104.666897, 28.806461], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区B6宿舍楼"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805492, Longitude: 104.675546, LocationJSON: `{"point": [104.675546, 28.805492], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805439, Longitude: 104.675547, LocationJSON: `{"point": [104.675547, 28.805439], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805413, Longitude: 104.675433, LocationJSON: `{"point": [104.675433, 28.805413], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805404, Longitude: 104.675699, LocationJSON: `{"point": [104.675699, 28.805404], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804578, Longitude: 104.674568, LocationJSON: `{"point": [104.674568, 28.804578], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804854, Longitude: 104.674417, LocationJSON: `{"point": [104.674417, 28.804854], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804981, Longitude: 104.674733, LocationJSON: `{"point": [104.674733, 28.804981], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804386, Longitude: 104.674967, LocationJSON: `{"point": [104.674967, 28.804386], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804738, Longitude: 104.674602, LocationJSON: `{"point": [104.674602, 28.804738], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804906, Longitude: 104.675305, LocationJSON: `{"point": [104.675305, 28.804906], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804649, Longitude: 104.674671, LocationJSON: `{"point": [104.674671, 28.804649], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804786, Longitude: 104.674527, LocationJSON: `{"point": [104.674527, 28.804786], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804483, Longitude: 104.674755, LocationJSON: `{"point": [104.674755, 28.804483], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学美术学院四川轻化工大学李白河校区", Latitude: 29.378223, Longitude: 104.830447, LocationJSON: `{"point": [104.830447, 29.378223], "address": "四川省自贡市大安区大山铺镇四川轻化工大学美术学院四川轻化工大学李白河校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804456, Longitude: 104.674805, LocationJSON: `{"point": [104.674805, 28.804456], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.803665, Longitude: 104.675376, LocationJSON: `{"point": [104.675376, 28.803665], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805394, Longitude: 104.675593, LocationJSON: `{"point": [104.675593, 28.805394], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805444, Longitude: 104.675437, LocationJSON: `{"point": [104.675437, 28.805444], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805398, Longitude: 104.675511, LocationJSON: `{"point": [104.675511, 28.805398], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805463, Longitude: 104.675515, LocationJSON: `{"point": [104.675515, 28.805463], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805453, Longitude: 104.675484, LocationJSON: `{"point": [104.675484, 28.805453], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805449, Longitude: 104.675512, LocationJSON: `{"point": [104.675512, 28.805449], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.80544, Longitude: 104.675518, LocationJSON: `{"point": [104.675518, 28.80544], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.80545, Longitude: 104.675506, LocationJSON: `{"point": [104.675506, 28.80545], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805344, Longitude: 104.675186, LocationJSON: `{"point": [104.675186, 28.805344], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道白塔路四川轻化工大学宜宾校区", Latitude: 28.802406, Longitude: 104.676428, LocationJSON: `{"point": [104.676428, 28.802406], "address": "四川省宜宾市翠屏区白沙湾街道白塔路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805424, Longitude: 104.675398, LocationJSON: `{"point": [104.675398, 28.805424], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805419, Longitude: 104.675173, LocationJSON: `{"point": [104.675173, 28.805419], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805342, Longitude: 104.675303, LocationJSON: `{"point": [104.675303, 28.805342], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804415, Longitude: 104.67534, LocationJSON: `{"point": [104.67534, 28.804415], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804721, Longitude: 104.674667, LocationJSON: `{"point": [104.674667, 28.804721], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804946, Longitude: 104.674707, LocationJSON: `{"point": [104.674707, 28.804946], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804588, Longitude: 104.674833, LocationJSON: `{"point": [104.674833, 28.804588], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804712, Longitude: 104.674637, LocationJSON: `{"point": [104.674637, 28.804712], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804683, Longitude: 104.674684, LocationJSON: `{"point": [104.674684, 28.804683], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道品正食府四川轻化工大学宜宾校区", Latitude: 28.804197, Longitude: 104.674306, LocationJSON: `{"point": [104.674306, 28.804197], "address": "四川省宜宾市翠屏区白沙湾街道品正食府四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804748, Longitude: 104.674629, LocationJSON: `{"point": [104.674629, 28.804748], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804744, Longitude: 104.674705, LocationJSON: `{"point": [104.674705, 28.804744], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804665, Longitude: 104.674662, LocationJSON: `{"point": [104.674662, 28.804665], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.804864, Longitude: 104.674618, LocationJSON: `{"point": [104.674618, 28.804864], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园", Latitude: 28.804764, Longitude: 104.67469, LocationJSON: `{"point": [104.67469, 28.804764], "address": "四川省宜宾市翠屏区白沙湾街道四川轻化工大学宜宾校区品正园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道干锅王(龙观嘉园店)龙观嘉园", Latitude: 28.803539, Longitude: 104.666359, LocationJSON: `{"point": [104.666359, 28.803539], "address": "四川省宜宾市翠屏区白沙湾街道干锅王(龙观嘉园店)龙观嘉园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道干锅王(龙观嘉园店)龙观嘉园", Latitude: 28.803638, Longitude: 104.666432, LocationJSON: `{"point": [104.666432, 28.803638], "address": "四川省宜宾市翠屏区白沙湾街道干锅王(龙观嘉园店)龙观嘉园"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805481, Longitude: 104.675397, LocationJSON: `{"point": [104.675397, 28.805481], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805465, Longitude: 104.675477, LocationJSON: `{"point": [104.675477, 28.805465], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
			{Address: "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区", Latitude: 28.805436, Longitude: 104.67548, LocationJSON: `{"point": [104.67548, 28.805436], "address": "四川省宜宾市翠屏区白沙湾街道大学路四川轻化工大学宜宾校区"}`},
		},
	},
	{
		Name: "李白河",
		Locations: []Location{
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377728, Longitude: 104.829171, LocationJSON: `{"point": [104.829171, 29.377728], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.37762, Longitude: 104.829322, LocationJSON: `{"point": [104.829322, 29.37762], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377374, Longitude: 104.829775, LocationJSON: `{"point": [104.829775, 29.377374], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377335, Longitude: 104.82811, LocationJSON: `{"point": [104.82811, 29.377335], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377333, Longitude: 104.828101, LocationJSON: `{"point": [104.828101, 29.377333], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.37879, Longitude: 104.832512, LocationJSON: `{"point": [104.832512, 29.37879], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377621, Longitude: 104.828983, LocationJSON: `{"point": [104.828983, 29.377621], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378843, Longitude: 104.832861, LocationJSON: `{"point": [104.832861, 29.378843], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.378293, Longitude: 104.828988, LocationJSON: `{"point": [104.828988, 29.378293], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378615, Longitude: 104.831166, LocationJSON: `{"point": [104.831166, 29.378615], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378629, Longitude: 104.832071, LocationJSON: `{"point": [104.832071, 29.378629], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378774, Longitude: 104.832531, LocationJSON: `{"point": [104.832531, 29.378774], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.379016, Longitude: 104.832771, LocationJSON: `{"point": [104.832771, 29.379016], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇东环路四川轻化工大学李白河校区", Latitude: 29.376971, Longitude: 104.827969, LocationJSON: `{"point": [104.827969, 29.376971], "address": "四川省自贡市大安区大山铺镇东环路四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377164, Longitude: 104.828087, LocationJSON: `{"point": [104.828087, 29.377164], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377599, Longitude: 104.829237, LocationJSON: `{"point": [104.829237, 29.377599], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377554, Longitude: 104.829284, LocationJSON: `{"point": [104.829284, 29.377554], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377544, Longitude: 104.829352, LocationJSON: `{"point": [104.829352, 29.377544], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377346, Longitude: 104.829682, LocationJSON: `{"point": [104.829682, 29.377346], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377662, Longitude: 104.829234, LocationJSON: `{"point": [104.829234, 29.377662], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377696, Longitude: 104.829252, LocationJSON: `{"point": [104.829252, 29.377696], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377042, Longitude: 104.82837, LocationJSON: `{"point": [104.82837, 29.377042], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇东环路四川轻化工大学李白河校区", Latitude: 29.376939, Longitude: 104.82829, LocationJSON: `{"point": [104.82829, 29.376939], "address": "四川省自贡市大安区大山铺镇东环路四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377047, Longitude: 104.828328, LocationJSON: `{"point": [104.828328, 29.377047], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.37745, Longitude: 104.828025, LocationJSON: `{"point": [104.828025, 29.37745], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377484, Longitude: 104.828586, LocationJSON: `{"point": [104.828586, 29.377484], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377458, Longitude: 104.8281, LocationJSON: `{"point": [104.8281, 29.377458], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377472, Longitude: 104.828129, LocationJSON: `{"point": [104.828129, 29.377472], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377084, Longitude: 104.828366, LocationJSON: `{"point": [104.828366, 29.377084], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学美术学院四川轻化工大学李白河校区", Latitude: 29.378304, Longitude: 104.832693, LocationJSON: `{"point": [104.832693, 29.378304], "address": "四川省自贡市大安区大山铺镇四川轻化工大学美术学院四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377728, Longitude: 104.829097, LocationJSON: `{"point": [104.829097, 29.377728], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377553, Longitude: 104.829348, LocationJSON: `{"point": [104.829348, 29.377553], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377627, Longitude: 104.829268, LocationJSON: `{"point": [104.829268, 29.377627], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区", Latitude: 29.377386, Longitude: 104.828149, LocationJSON: `{"point": [104.828149, 29.377386], "address": "四川省自贡市大安区大山铺镇艺雅苑1号楼四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇肯德基车速取餐点(四川轻化工大学店)四川轻化工大学李白河校区", Latitude: 29.376241, Longitude: 104.831257, LocationJSON: `{"point": [104.831257, 29.376241], "address": "四川省自贡市大安区大山铺镇肯德基车速取餐点(四川轻化工大学店)四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.37883, Longitude: 104.83262, LocationJSON: `{"point": [104.83262, 29.37883], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378814, Longitude: 104.832922, LocationJSON: `{"point": [104.832922, 29.378814], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377595, Longitude: 104.828464, LocationJSON: `{"point": [104.828464, 29.377595], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学美术学院四川轻化工大学李白河校区", Latitude: 29.378304, Longitude: 104.832692, LocationJSON: `{"point": [104.832692, 29.378304], "address": "四川省自贡市大安区大山铺镇四川轻化工大学美术学院四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378801, Longitude: 104.83255, LocationJSON: `{"point": [104.83255, 29.378801], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378916, Longitude: 104.833044, LocationJSON: `{"point": [104.833044, 29.378916], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.37725, Longitude: 104.828678, LocationJSON: `{"point": [104.828678, 29.37725], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.378875, Longitude: 104.832719, LocationJSON: `{"point": [104.832719, 29.378875], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377615, Longitude: 104.828745, LocationJSON: `{"point": [104.828745, 29.377615], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
			{Address: "四川省自贡市大安区大山铺镇东环路四川轻化工大学李白河校区", Latitude: 29.377195, Longitude: 104.827621, LocationJSON: `{"point": [104.827621, 29.377195], "address": "四川省自贡市大安区大山铺镇东环路四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区", Latitude: 29.379055, Longitude: 104.832944, LocationJSON: `{"point": [104.832944, 29.379055], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区"}`},
			{Address: "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑", Latitude: 29.377711, Longitude: 104.829025, LocationJSON: `{"point": [104.829025, 29.377711], "address": "四川省自贡市大安区大山铺镇四川轻化工大学李白河校区艺雅苑"}`},
		},
	},
	{
		Name: "汇东",
		Locations: []Location{
			{Address: "四川省自贡市自流井区学苑街道汇雅路15号四川轻化工大学汇东校区", Latitude: 29.330347, Longitude: 104.763952, LocationJSON: `{"point": [104.763952, 29.330347], "address": "四川省自贡市自流井区学苑街道汇雅路15号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道四川轻化工大学汇东校区学生公寓6栋", Latitude: 29.330216, Longitude: 104.763525, LocationJSON: `{"point": [104.763525, 29.330216], "address": "四川省自贡市自流井区学苑街道四川轻化工大学汇东校区学生公寓6栋"}`},
			{Address: "四川省自贡市自流井区学苑街道汇勤路四川轻化工大学汇东校区", Latitude: 29.331665, Longitude: 104.766355, LocationJSON: `{"point": [104.766355, 29.331665], "address": "四川省自贡市自流井区学苑街道汇勤路四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.329964, Longitude: 104.761283, LocationJSON: `{"point": [104.761283, 29.329964], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.330553, Longitude: 104.761095, LocationJSON: `{"point": [104.761095, 29.330553], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街紫萝香居", Latitude: 29.330652, Longitude: 104.760865, LocationJSON: `{"point": [104.760865, 29.330652], "address": "四川省自贡市自流井区学苑街道南苑街紫萝香居"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街25号四川轻化工大学汇东校区", Latitude: 29.330842, Longitude: 104.761191, LocationJSON: `{"point": [104.761191, 29.330842], "address": "四川省自贡市自流井区学苑街道南苑街25号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.330763, Longitude: 104.760958, LocationJSON: `{"point": [104.760958, 29.330763], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街27号四川轻化工大学汇东校区", Latitude: 29.331014, Longitude: 104.761287, LocationJSON: `{"point": [104.761287, 29.331014], "address": "四川省自贡市自流井区学苑街道南苑街27号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街27号四川轻化工大学汇东校区", Latitude: 29.330924, Longitude: 104.7612, LocationJSON: `{"point": [104.7612, 29.330924], "address": "四川省自贡市自流井区学苑街道南苑街27号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街27号四川轻化工大学汇东校区", Latitude: 29.331105, Longitude: 104.761194, LocationJSON: `{"point": [104.761194, 29.331105], "address": "四川省自贡市自流井区学苑街道南苑街27号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.330791, Longitude: 104.760997, LocationJSON: `{"point": [104.760997, 29.330791], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.330893, Longitude: 104.760824, LocationJSON: `{"point": [104.760824, 29.330893], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道汇智路四川轻化工大学汇东校区", Latitude: 29.333461, Longitude: 104.766039, LocationJSON: `{"point": [104.766039, 29.333461], "address": "四川省自贡市自流井区学苑街道汇智路四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道四川轻化工大学汇东校区学生公寓6栋", Latitude: 29.330286, Longitude: 104.763716, LocationJSON: `{"point": [104.763716, 29.330286], "address": "四川省自贡市自流井区学苑街道四川轻化工大学汇东校区学生公寓6栋"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.331095, Longitude: 104.760906, LocationJSON: `{"point": [104.760906, 29.331095], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街29号四川轻化工大学汇东校区", Latitude: 29.331285, Longitude: 104.761, LocationJSON: `{"point": [104.761, 29.331285], "address": "四川省自贡市自流井区学苑街道南苑街29号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街29号四川轻化工大学汇东校区", Latitude: 29.331123, Longitude: 104.761031, LocationJSON: `{"point": [104.761031, 29.331123], "address": "四川省自贡市自流井区学苑街道南苑街29号四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区", Latitude: 29.330785, Longitude: 104.761112, LocationJSON: `{"point": [104.761112, 29.330785], "address": "四川省自贡市自流井区学苑街道南苑街四川轻化工大学汇东校区"}`},
			{Address: "四川省自贡市自流井区学苑街道汇川路1637号四川轻化工大学汇东校区", Latitude: 29.330348, Longitude: 104.762494, LocationJSON: `{"point": [104.762494, 29.330348], "address": "四川省自贡市自流井区学苑街道汇川路1637号四川轻化工大学汇东校区"}`},
		},
	},
}

func All() []Campus {
	out := make([]Campus, 0, len(campuses))
	for _, campus := range campuses {
		cp := campus
		cp.Locations = append([]Location(nil), campus.Locations...)
		out = append(out, cp)
	}
	return out
}

func Summaries() []Summary {
	out := make([]Summary, 0, len(campuses))
	for _, campus := range campuses {
		out = append(out, Summary{Name: campus.Name, Count: len(campus.Locations)})
	}
	return out
}

func HasCampus(name string) bool {
	_, ok := Find(name)
	return ok
}

func Find(name string) (Campus, bool) {
	name = strings.TrimSpace(name)
	for _, campus := range campuses {
		if campus.Name == name {
			return campus, true
		}
	}
	return Campus{}, false
}

// Pick returns a random predefined point for the campus. Unknown campus names
// fall back to the source project's default campus.
func Pick(name string) (Location, bool) {
	campus, ok := Find(name)
	if !ok {
		campus, ok = Find(DefaultCampus)
	}
	if !ok || len(campus.Locations) == 0 {
		return Location{}, false
	}
	return campus.Locations[rand.IntN(len(campus.Locations))], true
}
