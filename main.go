package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/g8rswimmer/go-sfdc"
	"github.com/g8rswimmer/go-sfdc/credentials"
	"github.com/g8rswimmer/go-sfdc/session"
	"github.com/g8rswimmer/go-sfdc/sobject"
	"github.com/g8rswimmer/go-sfdc/sobject/collections"
	"github.com/g8rswimmer/go-sfdc/soql"
	"github.com/joho/godotenv"
	slackd "github.com/rusq/slackdump/v2"
	"github.com/rusq/slackdump/v2/auth"
)

type DML struct {
	SObject string                 `json:"sobject"`
	Fields  map[string]interface{} `json:"fields"`
	ID      string                 `json:"id,omitempty"`
}

type SlackRequestBody struct {
	Text string `json:"text"`
}

const (
	timeFormat = "2006-01-02 15:04:05"
)

var (
	from = time.Date(time.Now().Year(), time.September, 1, 0, 0, 0, 0, time.Local)
	to   = time.Now()
)

func init() {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatal("Error loading .env file:", err)
	}
}

func main() {
	session, err := jsonWebToken()
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}

	if err := generatePayloads(session); err != nil {
		log.Fatalf("Failed to generate payloads: %v", err)
	}
}

func generatePayloads(session *session.Session) error {
	provider, err := auth.NewValueAuth(os.Getenv("SLACK_TOKEN"), os.Getenv("SLACK_COOKIE"))
	if err != nil {
		return fmt.Errorf("failed to create Slack auth provider: %w", err)
	}

	sd, err := slackd.New(context.Background(), provider)
	if err != nil {
		return fmt.Errorf("failed to create Slack dumper: %w", err)
	}

	results, err := sd.Dump(context.Background(), os.Getenv("SLACK_CHANNELID"), from, to)
	if err != nil {
		return fmt.Errorf("failed to dump Slack messages: %w", err)
	}

	mqQueue, err := queryMQQueue("MQWebhookV1RestService", session)
	if err != nil {
		return fmt.Errorf("failed to query MQ queue: %w", err)
	}

	resource, err := collections.NewResources(session)
	if err != nil {
		return fmt.Errorf("failed to create Salesforce resource: %w", err)
	}

	var success, failed int
	var insertRecords []sobject.Inserter

	for _, s := range results.Messages {
		payload, err := createPayload(s.Message.Text, mqQueue)
		if err != nil {
			continue
		}
		insertRecords = append(insertRecords, payload)
	}

	saveResults, err := resource.Insert(true, insertRecords)
	if err != nil {
		return fmt.Errorf("failed to insert records: %w", err)
	}

	for _, saveResult := range saveResults {
		if saveResult.Success {
			success++
		} else {
			failed++
			if err := sendSlackNotification(os.Getenv("SLACK_WEBHOOK"), saveResult.Errors[0].Message); err != nil {
				log.Printf("Failed to send Slack notification: %v", err)
			}
		}
	}

	log.Printf("Inserted successfully %d records. Insert failed %d records.", success, failed)
	return nil
}

func createPayload(text string, mqQueue map[string]interface{}) (*DML, error) {
	splitArr := strings.Split(text, "event_detail:")
	if len(splitArr) < 2 {
		return nil, fmt.Errorf("invalid message format")
	}

	strJson := strings.ReplaceAll(splitArr[1], "'", "\"")
	formattedStrJson := strings.ReplaceAll(strJson, "False", "false")

	return &DML{
		SObject: "MQ_Payload__c",
		Fields: map[string]interface{}{
			"MQ_Queue__c": mqQueue["Id"].(string),
			"Name":        fmt.Sprintf("%s - Slack - %s", mqQueue["Name"].(string), time.Now().Format(timeFormat)),
			"Status__c":   "Queued",
			"Request__c":  formattedStrJson,
		},
	}, nil
}

func queryMQQueue(filter string, session *session.Session) (map[string]interface{}, error) {
	where, err := soql.WhereEquals("Name", filter)
	if err != nil {
		return nil, fmt.Errorf("SOQL Query Where Statement Error: %w", err)
	}

	input := soql.QueryInput{
		ObjectType: "MQ_Queue__c",
		FieldList:  []string{"Id", "Name"},
		Where:      where,
	}

	queryStmt, err := soql.NewQuery(input)
	if err != nil {
		return nil, fmt.Errorf("SOQL Query Statement Error: %w", err)
	}

	resource, err := soql.NewResource(session)
	if err != nil {
		return nil, fmt.Errorf("failed to create SOQL resource: %w", err)
	}

	result, err := resource.Query(queryStmt, false)
	if err != nil {
		return nil, fmt.Errorf("SOQL Query Error: %w", err)
	}

	if result.TotalSize() > 0 {
		return result.Records()[0].Record().Fields(), nil
	}
	return nil, nil
}

func jsonWebToken() (*session.Session, error) {
	privateKey, err := loadPrivateKey(os.Getenv("JWT_PATH"))
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	jwtCred, err := credentials.NewJWTCredentials(credentials.JwtCredentials{
		URL:            os.Getenv("SALESFORCE_URL"),
		ClientId:       os.Getenv("JWT_CLIENTID"),
		ClientUsername: os.Getenv("JWT_USER"),
		ClientKey:      privateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT credentials: %w", err)
	}

	config := sfdc.Configuration{
		Credentials: jwtCred,
		Client:      &http.Client{},
		Version:     50,
	}

	return session.Open(config)
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return jwt.ParseRSAPrivateKeyFromPEM(pemData)
}

func sendSlackNotification(webhookUrl, msg string) error {
	slackBody, err := json.Marshal(SlackRequestBody{Text: msg})
	if err != nil {
		return fmt.Errorf("failed to marshal Slack request body: %w", err)
	}

	resp, err := http.Post(webhookUrl, "application/json", bytes.NewBuffer(slackBody))
	if err != nil {
		return fmt.Errorf("failed to send Slack notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("non-ok response returned from Slack: %s", resp.Status)
	}
	return nil
}

func (d *DML) SObject() string { return d.SObject }
func (d *DML) Fields() map[string]interface{} { return d.Fields }
func (d *DML) ID() string { return d.ID }
