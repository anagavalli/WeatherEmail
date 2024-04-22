package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
	"github.com/pkg/errors"
)

const (
	SF_LAT                 = 37.75
	SF_LONG                = -122.43
	CHI_LAT                = 41.8781
	CHI_LONG               = -87.6298
	NWS_API_POINT_ENDPOINT = "https://api.weather.gov/points/"

	EMAIL_SENDER      = "arnesh.nagavalli46@gmail.com"
	EMAIL_RECIPIENT   = "arnesh.nagavalli46@gmail.com"
	EMAIL_SUBJECT_FMT = "Rain Reminder - %v"
	EMAIL_BODY_FMT    = `It's likely going to rain today. Maximum percipitation chance is %v%%.`
	EMAIL_CHAR_SET    = "UTF-8"
)

func filter[T any](s []T, test func(T) bool) (ret []T) {
	for _, elm := range s {
		if test(elm) {
			ret = append(ret, elm)
		}
	}
	return ret
}

// returns maximum percip chance
func getMaxPercipChance(lat string, long string) (int, error) {
	resp, err := http.Get(NWS_API_POINT_ENDPOINT + lat + "," + long)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from point endpoint: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response from point endpoint: %v", err)
	}

	pointResponse := struct {
		Properties struct {
			Forecast       string `json:"forecast"`
			ForecastHourly string `json:"forecastHourly"`
		} `json:"properties"`
	}{}
	json.Unmarshal(body, &pointResponse)
	resp, err = http.Get(pointResponse.Properties.ForecastHourly)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch from forecast endpoint: %v", err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response from forecast endpoint: %v", err)
	}

	type period struct {
		Name       string    `json:"name"`
		StartTime  time.Time `json:"startTime"`
		RainChance struct {
			Value int `json:"value"`
		} `json:"probabilityOfPrecipitation"`
	}

	forecastResponse := struct {
		Properties struct {
			Periods []period `json:"periods"`
		} `json:"properties"`
	}{}
	json.Unmarshal(body, &forecastResponse)
	// fmt.Println(forecastResponse)

	now := time.Now()
	forecastResponse.Properties.Periods = filter(forecastResponse.Properties.Periods, func(elm period) bool {
		loc := elm.StartTime.Local().Location()
		year, month, day := now.In(loc).Date()
		endOfDay := time.Date(year, month, day, 23, 59, 59, 0, loc)
		return elm.StartTime.Before(endOfDay)
	})

	maxPercipChance := 0
	for _, elm := range forecastResponse.Properties.Periods {
		if elm.RainChance.Value > maxPercipChance {
			maxPercipChance = elm.RainChance.Value
		}
	}
	return maxPercipChance, nil
}

func sendEmail(maxPercipChance int) error {
	// create new session, must be in region where SES config resides
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String("us-east-1")},
	)
	if err != nil {
		return err
	}
	// create an SES session
	svc := ses.New(sess)

	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(err)
	}
	today := time.Now().In(location).Format("01-02-2006")
	//assemble email
	input := &ses.SendEmailInput{
		Destination: &ses.Destination{
			ToAddresses: []*string{aws.String(EMAIL_RECIPIENT)},
		},
		Message: &ses.Message{
			Body: &ses.Body{
				Text: &ses.Content{
					Charset: aws.String(EMAIL_CHAR_SET),
					Data:    aws.String(fmt.Sprintf(EMAIL_BODY_FMT, maxPercipChance)),
				},
			},
			Subject: &ses.Content{
				Charset: aws.String(EMAIL_CHAR_SET),
				Data:    aws.String(fmt.Sprintf(EMAIL_SUBJECT_FMT, today)),
			},
		},
		Source: aws.String(EMAIL_SENDER),
	}
	//fmt.Println(input)

	// Attempt to send the email.
	_, err = svc.SendEmail(input)

	// Display error messages if they occur.
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ses.ErrCodeMessageRejected:
				err = errors.Wrapf(aerr, "%v", ses.ErrCodeMessageRejected)
			case ses.ErrCodeMailFromDomainNotVerifiedException:
				err = errors.Wrapf(aerr, "%v", ses.ErrCodeMailFromDomainNotVerifiedException)
			case ses.ErrCodeConfigurationSetDoesNotExistException:
				err = errors.Wrapf(aerr, "%v", ses.ErrCodeConfigurationSetDoesNotExistException)
			}
		}
		return err
	}
	fmt.Printf("email successfully sent to %v\n", EMAIL_RECIPIENT)
	return nil
}

type MyEvent struct {
}

func HandleRequest(ctx context.Context, event *MyEvent) (*string, error) {
	// SF
	maxPercipChance, err := getMaxPercipChance(fmt.Sprintf("%f", SF_LAT), fmt.Sprintf("%f", SF_LONG))
	// Chicago
	// maxPercipChance, err := getMaxPercipChance(fmt.Sprintf("%f", CHI_LAT), fmt.Sprintf("%f", CHI_LONG))
	if err != nil {
		return nil, err
	}
	fmt.Printf("maxPercipChance: %v%%\n", maxPercipChance)

	if maxPercipChance > 35 {
		err = sendEmail(maxPercipChance)
		if err != nil {
			return nil, err
		}
	}
	return aws.String("success"), nil
}

func main() {
	lambda.Start(HandleRequest)
}
