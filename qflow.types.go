package main

type FilterSort struct {
	QueId    int    `json:"queId"`
	IsAscend bool   `json:"isAscend"`
	QueType  int    `json:"queType"`
	SortType string `json:"sortType,omitempty"`
}

var updateTimeSorter = FilterSort{
	QueId:    3,
	IsAscend: false,
	QueType:  4,
}

type Filter struct {
	PageSize int           `json:"pageSize"`
	PageNum  int           `json:"pageNum"`
	Type     int           `json:"type"`
	Sorts    []FilterSort  `json:"sorts"`
	Queries  []interface{} `json:"queries"`
	QueryKey interface{}   `json:"queryKey"`
}

type FilterRequest struct {
	Filter Filter `json:"filter"`
}

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type FilterResponse struct {
	Response
	Data FilterResponseData `json:"data"`
}

type FilterResponseDataItemAnswer struct {
	AssociatedQueType           interface{}   `json:"associatedQueType"`
	AuditValueDescription       interface{}   `json:"auditValueDescription"`
	BeingCanDecrypted           bool          `json:"beingCanDecrypted"`
	BeingDesensitized           bool          `json:"beingDesensitized"`
	DateType                    int           `json:"dateType"`
	PreviousTableRowOrdinalList []interface{} `json:"previousTableRowOrdinalList"`
	QueId                       int           `json:"queId"`
	QueTitle                    string        `json:"queTitle"`
	QueType                     int           `json:"queType"`
	QuestionOrdinal             int           `json:"questionOrdinal"`
	ReferValues                 []interface{} `json:"referValues"`
	SupId                       interface{}   `json:"supId"`
	TableValues                 []interface{} `json:"tableValues"`
	Values                      []struct {
		DataValue   string      `json:"dataValue"`
		Email       interface{} `json:"email"`
		Id          *int        `json:"id"`
		OptionId    interface{} `json:"optionId"`
		Ordinal     interface{} `json:"ordinal"`
		OtherInfo   *string     `json:"otherInfo"`
		PluginValue interface{} `json:"pluginValue"`
		QueId       int         `json:"queId"`
		Value       string      `json:"value"`
	} `json:"values"`
}

type FilterResponseDataItem struct {
	Answers       []FilterResponseDataItemAnswer `json:"answers"`
	ApplyBaseInfo struct {
		ApplyNum  string `json:"applyNum"`
		ApplyTime string `json:"applyTime"`
		ApplyUser struct {
			Accepted             interface{} `json:"accepted"`
			Auth                 interface{} `json:"auth"`
			BeingThirdPartyAdmin interface{} `json:"beingThirdPartyAdmin"`
			Email                interface{} `json:"email"`
			HeadImg              *string     `json:"headImg"`
			MobileNum            interface{} `json:"mobileNum"`
			NickName             interface{} `json:"nickName"`
			Remark               string      `json:"remark"`
			Status               interface{} `json:"status"`
			Uid                  *int        `json:"uid"`
			UserId               interface{} `json:"userId"`
			UserName             string      `json:"userName"`
			WxOpenId             interface{} `json:"wxOpenId"`
		} `json:"applyUser"`
		CurrentAuditNodes []struct {
			AuditNodeId    int         `json:"auditNodeId"`
			AuditNodeName  string      `json:"auditNodeName"`
			PreTimeoutDate interface{} `json:"preTimeoutDate"`
			TimeoutDate    interface{} `json:"timeoutDate"`
		} `json:"currentAuditNodes"`
		DataSource     interface{} `json:"dataSource"`
		FormTitle      string      `json:"formTitle"`
		LastUpdateTime string      `json:"lastUpdateTime"`
	} `json:"applyBaseInfo"`
	ApplyId          int         `json:"applyId"`
	ApplyNum         int         `json:"applyNum"`
	ApplyStatus      int         `json:"applyStatus"`
	ApplyUid         *int        `json:"applyUid"`
	AuditNodeId      interface{} `json:"auditNodeId"`
	AuditNodeTime    interface{} `json:"auditNodeTime"`
	BeingDeleted     bool        `json:"beingDeleted"`
	BeingUnread      bool        `json:"beingUnread"`
	BeingUrged       bool        `json:"beingUrged"`
	CreateTime       interface{} `json:"createTime"`
	CustomApplyNum   interface{} `json:"customApplyNum"`
	DeleteId         interface{} `json:"deleteId"`
	DeleteType       interface{} `json:"deleteType"`
	DeptInfo         interface{} `json:"deptInfo"`
	LastUpdateTime   int64       `json:"lastUpdateTime"`
	WechatInfoStatus bool        `json:"wechatInfoStatus"`
}

type FilterResponseData struct {
	List     []FilterResponseDataItem `json:"list"`
	PageNum  int                      `json:"pageNum"`
	PageSize int                      `json:"pageSize"`
	Total    int                      `json:"total"`
}
