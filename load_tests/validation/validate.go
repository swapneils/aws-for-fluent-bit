package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	envAWSRegion   = "AWS_REGION"
	envS3Bucket    = "S3_BUCKET_NAME"
	envCWLogGroup  = "CW_LOG_GROUP_NAME"
	envLogPrefix   = "LOG_PREFIX"
	envDestination = "DESTINATION"
	idCounterBase  = 10000000
)

type Message struct {
	Log string
}

func main() {
	region := os.Getenv(envAWSRegion)
	if region == "" {
		exitErrorf("[TEST FAILURE] AWS Region required. Set the value for environment variable- %s", envAWSRegion)
	}

	bucket := os.Getenv(envS3Bucket)
	if bucket == "" {
		exitErrorf("[TEST FAILURE] Bucket name required. Set the value for environment variable- %s", envS3Bucket)
	}

	logGroup := os.Getenv(envCWLogGroup)
	if logGroup == "" {
		exitErrorf("[TEST FAILURE] Log group name required. Set the value for environment variable- %s", envCWLogGroup)
	}

	prefix := os.Getenv(envLogPrefix)
	if prefix == "" {
		exitErrorf("[TEST FAILURE] Object prefix required. Set the value for environment variable- %s", envLogPrefix)
	}

	destination := os.Getenv(envDestination)
	if destination == "" {
		exitErrorf("[TEST FAILURE] Log destination for validation required. Set the value for environment variable- %s", envDestination)
	}

	inputRecord := os.Args[1]
	if inputRecord == "" {
		exitErrorf("[TEST FAILURE] Total input record number required. Set the value as the first argument")
	}
	totalInputRecord, _ := strconv.Atoi((inputRecord))
	// Map for counting unique records in corresponding destination
	inputMap := make(map[string]bool)
	for i := 0; i < totalInputRecord; i++ {
		recordId := strconv.Itoa(idCounterBase + i)
		inputMap[recordId] = false
	}

	logDelay := os.Args[2]
	if logDelay == "" {
		exitErrorf("[TEST FAILURE] Log delay required. Set the value as the second argument")
	}

	totalRecordFound := 0
	if destination == "s3" {
		s3Client, err := getS3Client(region)
		if err != nil {
			exitErrorf("[TEST FAILURE] Unable to create new S3 client: %v", err)
		}

		totalRecordFound, inputMap = validate_s3(s3Client, bucket, prefix, inputMap)
	} else if destination == "cloudwatch" {
		cwClient, err := getCWClient(region)
		if err != nil {
			exitErrorf("[TEST FAILURE] Unable to create new CloudWatch client: %v", err)
		}

		totalRecordFound, inputMap = validate_cloudwatch(cwClient, logGroup, prefix, inputMap)
	}

	// Get benchmark results based on log loss, log delay and log duplication
	get_results(totalInputRecord, totalRecordFound, inputMap, logDelay)
}

// Creates a new S3 Client
func getS3Client(region string) (*s3.S3, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)

	if err != nil {
		return nil, err
	}

	return s3.New(sess), nil
}

// Validates the log messages. Our log producer is designed to write log records in a specific format.
// Log format generated by our producer: 8CharUniqueID_13CharTimestamp_RandomString (10029999_1639151827578_RandomString).
// Both of the Kinesis Streams and Kinesis Firehose try to send each log maintaining the "at least once" policy.
// To validate, we need to make sure all the log records from input file are stored at least once.
func validate_s3(s3Client *s3.S3, bucket string, prefix string, inputMap map[string]bool) (int, map[string]bool) {
	var continuationToken *string
	var input *s3.ListObjectsV2Input
	s3RecordCounter := 0
	s3ObjectCounter := 0

	// Returns all the objects from a S3 bucket with the given prefix.
	// This approach utilizes NextContinuationToken to pull all the objects from the S3 bucket.
	for {
		input = &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: continuationToken,
			Prefix:            aws.String(prefix),
		}

		response, err := s3Client.ListObjectsV2(input)
		if err != nil {
			exitErrorf("[TEST FAILURE] Error occured to get the objects from bucket: %q., %v", bucket, err)
		}

		for _, content := range response.Contents {
			input := &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    content.Key,
			}
			obj := getS3Object(s3Client, input)
			s3ObjectCounter++

			dataByte, err := ioutil.ReadAll(obj.Body)
			if err != nil {
				exitErrorf("[TEST FAILURE] Error to parse GetObject response. %v", err)
			}

			data := strings.Split(string(dataByte), "\n")

			for _, d := range data {
				if d == "" {
					continue
				}

				var message Message

				decodeError := json.Unmarshal([]byte(d), &message)
				if decodeError != nil {
					exitErrorf("[TEST FAILURE] Json Unmarshal Error:", decodeError)
				}

				// First 8 char is the unique record ID
				recordId := message.Log[:8]
				s3RecordCounter += 1
				if _, ok := inputMap[recordId]; ok {
					// Setting true to indicate that this record was found in the destination
					inputMap[recordId] = true
				}
			}
		}

		if !aws.BoolValue(response.IsTruncated) {
			break
		}
		continuationToken = response.NextContinuationToken
	}

	fmt.Println("Total object in S3: ", s3ObjectCounter)

	return s3RecordCounter, inputMap
}

// Retrieves an object from a S3 bucket
func getS3Object(s3Client *s3.S3, input *s3.GetObjectInput) *s3.GetObjectOutput {
	obj, err := s3Client.GetObject(input)

	if err != nil {
		exitErrorf("[TEST FAILURE] Error occured to get s3 object: %v", err)
	}

	return obj
}

// Creates a new CloudWatch Client
func getCWClient(region string) (*cloudwatchlogs.CloudWatchLogs, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)

	if err != nil {
		return nil, err
	}

	return cloudwatchlogs.New(sess), nil
}

// Validate logs in CloudWatch.
// Similar logic as S3 validation.
func validate_cloudwatch(cwClient *cloudwatchlogs.CloudWatchLogs, logGroup string, logStream string, inputMap map[string]bool) (int, map[string]bool) {
	var forwardToken *string
	var input *cloudwatchlogs.GetLogEventsInput
	cwRecoredCounter := 0

	// Returns all log events from a CloudWatch log group with the given log stream.
	// This approach utilizes NextForwardToken to pull all log events from the CloudWatch log group.
	for {
		if forwardToken == nil {
			input = &cloudwatchlogs.GetLogEventsInput{
				LogGroupName:  aws.String(logGroup),
				LogStreamName: aws.String(logStream),
				StartFromHead: aws.Bool(true),
			}
		} else {
			input = &cloudwatchlogs.GetLogEventsInput{
				LogGroupName:  aws.String(logGroup),
				LogStreamName: aws.String(logStream),
				NextToken:     forwardToken,
				StartFromHead: aws.Bool(true),
			}
		}

		response, err := cwClient.GetLogEvents(input)
		for err != nil {
			// retry for throttling exception
			if strings.Contains(err.Error(), "ThrottlingException: Rate exceeded") {
				time.Sleep(1 * time.Second)
				response, err = cwClient.GetLogEvents(input)
			} else {
				exitErrorf("[TEST FAILURE] Error occured to get the log events from log group: %q., %v", logGroup, err)
			}
		}

		for _, event := range response.Events {
			log := aws.StringValue(event.Message)

			// First 8 char is the unique record ID
			recordId := log[:8]
			cwRecoredCounter += 1
			if _, ok := inputMap[recordId]; ok {
				// Setting true to indicate that this record was found in the destination
				inputMap[recordId] = true
			}
		}

		// Same NextForwardToken will be returned if we reach the end of the log stream
		if aws.StringValue(response.NextForwardToken) == aws.StringValue(forwardToken) {
			break
		}

		forwardToken = response.NextForwardToken
	}

	return cwRecoredCounter, inputMap
}

func get_results(totalInputRecord int, totalRecordFound int, recordMap map[string]bool, logDelay string) {
	uniqueRecordFound := 0
	// Count how many unique records were found in the destination
	for _, v := range recordMap {
		if v {
			uniqueRecordFound++
		}
	}

	fmt.Println("Total input record: ", totalInputRecord)
	fmt.Println("Total record in destination: ", totalRecordFound)
	fmt.Println("Unique record in destination: ", uniqueRecordFound)
	fmt.Println("Duplicate records: ", (totalRecordFound - uniqueRecordFound))
	fmt.Println("Log Delay: ", logDelay)
	fmt.Println("Log Loss: ", (totalInputRecord-uniqueRecordFound)*100/totalInputRecord, "%")

	if totalInputRecord != uniqueRecordFound {
		fmt.Println("Number of missing log records: ", totalInputRecord-uniqueRecordFound)
	}
}

func exitErrorf(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, msg+"\n", args...)
	os.Exit(1)
}