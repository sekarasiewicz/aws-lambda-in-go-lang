package main

import (
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbiface"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
)

var (
	dynaClient dynamodbiface.DynamoDBAPI
)

func main() {
	region := os.Getenv("AWS_REGION")
	awsSession, err := session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)
	if err != nil {
		log.Println("AWS session could not be created")
		return
	}
	dynaClient = dynamodb.New(awsSession)
	lambda.Start(handler)
}

type User struct {
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

const tableName = "LambdaInGoUser"

type ErrorBody struct {
	ErrorMsg *string `json:"error,omitempty"`
}

var (
	ErrorInvalidUserData         = "Invalid User Data"
	ErrorMethodNotAllowed        = "Method Not Allowed"
	ErrorFailedToUnmarshalRecord = "Failed to unmarshal record"
	ErrorInvalidEmail            = "Invalid Email"
	ErrorFailedToFetchRecord     = "Failed to fetch record"
	ErrorCouldNotMarshalItem     = "Could not marshal item"
	ErrorCouldNotDeleteItem      = "Could not delete item"
	ErrorCouldNotDynamoPutItem   = "Could not Dynamo Put Item Error"
	ErrorUserAlreadyExists       = "User already exists"
	ErrorUserDoesNotExists       = "User does not exist"
)

func APIResponse(status int, body interface{}) *events.APIGatewayProxyResponse {
	resp := events.APIGatewayProxyResponse{Headers: map[string]string{"Content-Type": "application/json"}}
	resp.StatusCode = status

	stringBody, _ := json.Marshal(body)
	resp.Body = string(stringBody)
	return &resp
}

func isEmailValid(email string) bool {
	var rxEmail = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]{1,64}@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

	if len(email) < 3 || len(email) > 254 || !rxEmail.MatchString(email) {
		return false
	}
	return true
}

func showUser(email string) (*events.APIGatewayProxyResponse, error) {
	user, err := fetchUser(email)
	if err != nil {
		return APIResponse(http.StatusBadRequest, err), nil
	}

	return APIResponse(http.StatusOK, user), nil
}

func listUsers() (*events.APIGatewayProxyResponse, error) {
	input := &dynamodb.ScanInput{
		TableName: aws.String(tableName),
	}
	result, err := dynaClient.Scan(input)
	if err != nil {
		log.Println(err)
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorFailedToFetchRecord,
		}), nil
	}
	log.Printf("%+v", result)
	return APIResponse(http.StatusOK, result), nil
}

func getUser(req events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	email := req.QueryStringParameters["email"]
	log.Println("Email Kind", reflect.TypeOf(email).Kind())
	if len(email) > 0 {
		return showUser(email)
	}
	return listUsers()
}

func fetchUser(email string) (*User, *ErrorBody) {
	input := &dynamodb.GetItemInput{
		Key: map[string]*dynamodb.AttributeValue{
			"email": {
				S: aws.String(email),
			},
		},
		TableName: aws.String(tableName),
	}

	result, err := dynaClient.GetItem(input)
	if err != nil {
		log.Println(err.Error())
		return nil, &ErrorBody{
			&ErrorFailedToFetchRecord,
		}
	}

	item := User{}
	err = dynamodbattribute.UnmarshalMap(result.Item, &item)
	if err != nil {
		log.Printf("Failed to unmarshal Record, %v", err)
		return nil, &ErrorBody{
			&ErrorFailedToUnmarshalRecord,
		}
	}
	return &item, nil
}

func createUser(req events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	var user User
	if err := json.Unmarshal([]byte(req.Body), &user); err != nil {
		log.Printf("Invalid json format: %v", err.Error())
		return APIResponse(http.StatusUnprocessableEntity, ErrorBody{&ErrorInvalidUserData}), nil
	}
	if !isEmailValid(user.Email) {
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorInvalidEmail,
		}), nil
	}
	// Check if user exists
	currentUser, _ := fetchUser(user.Email)
	log.Println("currentUser: ", currentUser)
	if len(currentUser.Email) != 0 {
		return APIResponse(http.StatusBadRequest, ErrorBody{&ErrorUserAlreadyExists}), nil
	}
	// Save user

	av, err := dynamodbattribute.MarshalMap(user)
	if err != nil {
		log.Println("Could not marshal item: ", err.Error())
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorCouldNotMarshalItem,
		}), nil
	}

	input := &dynamodb.PutItemInput{
		Item:      av,
		TableName: aws.String(tableName),
	}

	result, err := dynaClient.PutItem(input)
	if err != nil {
		log.Println("Dynamo Put Item Error: ", err.Error())
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorCouldNotDynamoPutItem,
		}), nil
	}
	log.Println("Result: ", result)
	return APIResponse(http.StatusCreated, nil), nil
}

func updateUser(req events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	var user User
	if err := json.Unmarshal([]byte(req.Body), &user); err != nil {
		log.Printf("Invalid json format: %v", err.Error())
		return APIResponse(http.StatusUnprocessableEntity, ErrorBody{&ErrorInvalidEmail}), nil
	}

	// Check if user exists
	currentUser, _ := fetchUser(user.Email)
	log.Println("Update Current User", currentUser)
	if len(currentUser.Email) == 0 {
		return APIResponse(http.StatusBadRequest, ErrorBody{&ErrorUserDoesNotExists}), nil
	}
	// Save user

	av, err := dynamodbattribute.MarshalMap(user)
	if err != nil {
		log.Println("Could not marshal item: ", err.Error())
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorCouldNotMarshalItem,
		}), nil
	}

	input := &dynamodb.PutItemInput{
		Item:      av,
		TableName: aws.String(tableName),
	}

	result, err := dynaClient.PutItem(input)
	if err != nil {
		log.Println("Dynamo Put Item Error: ", err.Error())
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorCouldNotDynamoPutItem,
		}), nil
	}
	log.Println("Result: ", result)
	return APIResponse(http.StatusCreated, nil), nil
}

func deleteUser(req events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	email := req.QueryStringParameters["email"]
	log.Println("email", email)
	input := &dynamodb.DeleteItemInput{
		Key: map[string]*dynamodb.AttributeValue{
			"email": {
				S: aws.String(email),
			},
		},
		TableName: aws.String(tableName),
	}
	result, err := dynaClient.DeleteItem(input)
	if err != nil {
		log.Println("Could not remove item ", err.Error())
		return APIResponse(http.StatusBadRequest, ErrorBody{
			&ErrorCouldNotDeleteItem,
		}), nil
	}

	return APIResponse(http.StatusOK, result), nil
}

func handler(req events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {
	log.Printf("REQ: %+v", req)
	switch req.HTTPMethod {
	case "GET":
		return getUser(req)
	case "POST":
		return createUser(req)
	case "PUT":
		return updateUser(req)
	case "DELETE":
		return deleteUser(req)
	default:
		return APIResponse(http.StatusMethodNotAllowed, ErrorBody{&ErrorMethodNotAllowed}), nil
	}
}
