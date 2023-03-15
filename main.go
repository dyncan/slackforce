package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/g8rswimmer/go-sfdc"
	"github.com/g8rswimmer/go-sfdc/bulk"
	"github.com/g8rswimmer/go-sfdc/credentials"
	"github.com/g8rswimmer/go-sfdc/session"
	"github.com/g8rswimmer/go-sfdc/sobject"
	"github.com/g8rswimmer/go-sfdc/sobject/collections"
	"github.com/g8rswimmer/go-sfdc/soql"
	"github.com/joho/godotenv"
	slackd "github.com/rusq/slackdump/v2"
	"github.com/rusq/slackdump/v2/auth"
)

type dml struct {
	sobject string
	fields  map[string]interface{}
	id      string
}

type SlackRequestBody struct {
	Text string `json:"text"`
}

var (
	from = time.Date(time.Now().Year(), time.September, 1, 00, 00, 00, 00, time.Local)
	to   = time.Now()
)

func init() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

func main() {

	//password 认证
	//session, err := password()

	//JWT 认证
	session, err := jsonWebToken()
	if err != nil {
		fmt.Printf("Authentication failed %s\n", err.Error())
		return
	}

	// 创建payload数据并同步到Salesforce
	generatePayloads(session)
}

// 构建MQ Payload数据
func generatePayloads(session *session.Session) {
	//存待创建的payload list数据

	provider, err := auth.NewValueAuth(os.Getenv("SLACK_TOKEN"), os.Getenv("SLACK_COOKIE"))
	if err != nil {
		log.Print(err)
		return
	}
	sd, err := slackd.New(context.Background(), provider)
	if err != nil {
		log.Print(err)
		return
	}

	//根据频道ID获取Chat History
	results, err := sd.Dump(context.Background(), os.Getenv("SLACK_CHANNELID"), from, to)
	if err != nil {
		log.Print(err)
		return
	}

	mqQueue, err := queryMQQueue("MQWebhookV1RestService", session)
	if err != nil || mqQueue == nil {
		log.Print(err)
		return
	}

	success := 0
	failed := 0

	// 循环chat history并解析event detail
	for _, s := range results.Messages {
		str := s.Message.Text
		splitArr := strings.Split(str, "event_detail:")
		if len(splitArr) >= 2 {

			var insertRecords []sobject.Inserter

			//解析topic name
			strJson := strings.ReplaceAll(splitArr[1], "'", "\"")
			formattedStrJson := strings.ReplaceAll(strJson, "False", "false")

			//构建Payload data structure
			payload := &dml{
				sobject: "MQ_Payload__c",
				fields: map[string]interface{}{
					"MQ_Queue__c": mqQueue["Id"].(string),
					"Name":        mqQueue["Name"].(string) + " - Slack - " + time.Now().Format("2006-01-02 03:04:05"),
					"Status__c":   "Queued",
					//"Status__c":   "Aborted",
					"Request__c": formattedStrJson,
				},
			}
			insertRecords = append(insertRecords, payload)

			resource, _ := collections.NewResources(session)
			saveResults, _ := resource.Insert(true, insertRecords)

			for _, saveResult := range saveResults {
				if saveResult.Success {
					success++
				} else {
					failed++
					webhookUrl := os.Getenv("SLACK_WEBHOOK")
					syncResult := formattedStrJson
					SendSlackNotification(webhookUrl, syncResult)
				}
			}
		}
	}

	log.Println("Inserted successfully ", success, " records.")
	log.Println("Insert failed ", failed, " records.")
}

// 基于过滤条件获取MQ记录
func queryMQQueue(filter string, session *session.Session) (map[string]interface{}, error) {

	where, err := soql.WhereEquals("Name", filter)
	if err != nil {
		fmt.Printf("SOQL Query Where Statement Error %s\n", err.Error())
		return nil, err
	}
	input := soql.QueryInput{
		ObjectType: "MQ_Queue__c",
		FieldList: []string{
			"Id",
			"Name",
		},
		Where: where,
	}
	queryStmt, err := soql.NewQuery(input)
	if err != nil {
		fmt.Printf("SOQL Query Statement Error %s\n", err.Error())
		return nil, err
	}

	resource, _ := soql.NewResource(session)
	result, err := resource.Query(queryStmt, false)
	if err != nil {
		fmt.Printf("SOQL Query Error %s\n", err.Error())
		return nil, err
	}
	if result.TotalSize() > 0 {
		return result.Records()[0].Record().Fields(), nil
	}
	return nil, nil
}

func bulkOperation(session *session.Session) {
	bulk.NewResource(session)
}

// 密码方法认证
func password() (*session.Session, error) {

	pwdCreds, authErr := credentials.NewPasswordCredentials(credentials.PasswordCredentials{
		URL:          os.Getenv("SALESFORCE_URL"),
		Username:     os.Getenv("USERNAME"),
		Password:     os.Getenv("PASSWORD"),
		ClientID:     os.Getenv("CLIENTID"),
		ClientSecret: os.Getenv("CLIENTSECRET"),
	})

	if authErr != nil {
		fmt.Printf("error %v\n", authErr)
		return nil, authErr
	}

	config := sfdc.Configuration{
		Credentials: pwdCreds,
		Client:      http.DefaultClient,
		Version:     50,
	}

	session, sessionErr := session.Open(config)
	if sessionErr != nil {
		fmt.Printf("error %v\n", sessionErr)
		return nil, sessionErr
	}

	return session, nil
}

// JWT方式认证
func jsonWebToken() (*session.Session, error) {
	// 读取server key文件
	privateKeyFile, err := os.Open(os.Getenv("JWT_PATH"))
	if err != nil {
		return nil, err
	}
	pemfileinfo, _ := privateKeyFile.Stat()
	var size = pemfileinfo.Size()
	pembytes := make([]byte, size)
	buffer := bufio.NewReader(privateKeyFile)
	_, err = buffer.Read(pembytes)
	pemData := pembytes
	err = privateKeyFile.Close()
	if err != nil {
		return nil, err
	} // 文件关闭
	signKey, err := jwt.ParseRSAPrivateKeyFromPEM(pemData)

	// 构建 credentials
	jwtCred, authErr := credentials.NewJWTCredentials(credentials.JwtCredentials{
		URL:            os.Getenv("SALESFORCE_URL"),
		ClientId:       os.Getenv("JWT_CLIENTID"),
		ClientUsername: os.Getenv("JWT_USER"),
		ClientKey:      signKey,
	})

	if authErr != nil {
		fmt.Printf("error %v\n", authErr)
		return nil, authErr
	}

	config := sfdc.Configuration{
		Credentials: jwtCred,
		Client:      &http.Client{},
		Version:     50,
	}

	session, sessionErr := session.Open(config)
	if sessionErr != nil {
		fmt.Printf("error %v\n", sessionErr)
		return nil, sessionErr
	}

	return session, nil
}

// 发送Slack通知
func SendSlackNotification(webhookUrl string, msg string) error {

	slackBody, _ := json.Marshal(SlackRequestBody{Text: msg})
	req, err := http.NewRequest(http.MethodPost, webhookUrl, bytes.NewBuffer(slackBody))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if buf.String() != "ok" {
		return errors.New("Non-ok response returned from Slack")
	}
	return nil
}

func (d *dml) SObject() string {
	return d.sobject
}
func (d *dml) Fields() map[string]interface{} {
	return d.fields
}
func (d *dml) ID() string {
	return d.id
}

type query struct {
	sobject string
	id      string
	fields  []string
}

func (q *query) SObject() string {
	return q.sobject
}
func (q *query) ID() string {
	return q.id
}
func (q *query) Fields() []string {
	return q.fields
}
