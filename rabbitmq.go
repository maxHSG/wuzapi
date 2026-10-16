package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog/log"
)

var (
	rabbitConn    *amqp091.Connection
	rabbitChannel *amqp091.Channel
	rabbitEnabled bool
	rabbitQueue   string
)

const retryInterval = 3 * time.Second

func rabbitMQRequired() bool {
	return os.Getenv("RABBITMQ_URL") != ""
}

func declareDefaultQueue(channel *amqp091.Channel) error {
	_, err := channel.QueueDeclare(
		rabbitQueue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	)
	return err
}

func connectRabbitMQ() (*amqp091.Connection, *amqp091.Channel, error) {
	conn, err := amqp091.Dial(os.Getenv("RABBITMQ_URL"))
	if err != nil {
		return nil, nil, err
	}
	channel, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if err := declareDefaultQueue(channel); err != nil {
		channel.Close()
		conn.Close()
		return nil, nil, err
	}
	return conn, channel, nil
}

func setRabbitMQConnection(conn *amqp091.Connection, channel *amqp091.Channel) {
	rabbitConn = conn
	rabbitChannel = channel
	rabbitEnabled = true
}

// InitRabbitMQ must run before the HTTP server starts.
// If RABBITMQ_URL is set, it blocks until the broker accepts AMQP (e.g. after host reboot).
func InitRabbitMQ() {
	rabbitURL := os.Getenv("RABBITMQ_URL")
	rabbitQueue = os.Getenv("RABBITMQ_QUEUE")

	if rabbitQueue == "" {
		rabbitQueue = "whatsapp_events"
	}

	if rabbitURL == "" {
		rabbitEnabled = false
		log.Info().Msg("RABBITMQ_URL is not set. RabbitMQ publishing disabled.")
		return
	}

	log.Info().
		Str("queue", rabbitQueue).
		Msg("RabbitMQ required: waiting for broker before starting server")

	for attempt := 1; ; attempt++ {
		conn, channel, err := connectRabbitMQ()
		if err != nil {
			log.Warn().
				Err(err).
				Int("attempt", attempt).
				Dur("retry_in", retryInterval).
				Msg("RabbitMQ not ready yet")
			time.Sleep(retryInterval)
			continue
		}

		setRabbitMQConnection(conn, channel)

		log.Info().
			Str("queue", rabbitQueue).
			Int("attempt", attempt).
			Msg("RabbitMQ connection established successfully")

		go handleConnectionErrors()
		return
	}
}

func handleConnectionErrors() {
	notifyClose := rabbitConn.NotifyClose(make(chan *amqp091.Error))

	for err := range notifyClose {
		log.Error().
			Err(err).
			Msg("RabbitMQ connection closed unexpectedly. Attempting reconnection...")

		rabbitEnabled = false

		for attempt := 1; ; attempt++ {
			time.Sleep(retryInterval)

			conn, channel, err := connectRabbitMQ()
			if err != nil {
				log.Warn().
					Err(err).
					Int("attempt", attempt).
					Msg("RabbitMQ reconnection failed")
				continue
			}

			setRabbitMQConnection(conn, channel)

			log.Info().Int("attempt", attempt).Msg("RabbitMQ reconnected successfully")

			go handleConnectionErrors()
			return
		}
	}
}

func PublishToRabbit(data []byte, queueOverride ...string) error {
	if !rabbitEnabled {
		return nil
	}
	queueName := rabbitQueue
	if len(queueOverride) > 0 && queueOverride[0] != "" {
		queueName = queueOverride[0]
	}
	_, err := rabbitChannel.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Error().Err(err).Str("queue", queueName).Msg("Could not declare RabbitMQ queue")
		return err
	}
	err = rabbitChannel.Publish(
		"",
		queueName,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			Body:         data,
			DeliveryMode: amqp091.Persistent,
		},
	)
	if err != nil {
		log.Error().Err(err).Str("queue", queueName).Msg("Could not publish to RabbitMQ")
	} else {
		log.Debug().Str("queue", queueName).Msg("Published message to RabbitMQ")
	}
	return err
}

func sendToGlobalRabbit(jsonData []byte, token string, userID string, queueName ...string) {
	if !rabbitEnabled {
		if rabbitMQRequired() {
			log.Error().Msg("RabbitMQ is configured but temporarily unavailable. Event not published to queue.")
		} else {
			log.Debug().Msg("RabbitMQ not configured. Event not published to queue.")
		}
		return
	}

	instance_name := ""
	userinfo, found := userinfocache.Get(token)
	if found {
		instance_name = userinfo.(Values).Get("Name")
	}

	var originalData map[string]interface{}
	err := json.Unmarshal(jsonData, &originalData)
	if err != nil {
		log.Error().Err(err).Msg("Failed to unmarshal original JSON data for RabbitMQ")
		return
	}

	originalData["userID"] = userID
	originalData["instanceName"] = instance_name

	enhancedJSON, err := json.Marshal(originalData)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal enhanced data for RabbitMQ")
		return
	}

	err = PublishToRabbit(enhancedJSON, queueName...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to publish to RabbitMQ")
	}
}

func PublishFileErrorToQueue(payload WebhookFileErrorPayload) {
	queueName := *webhookErrorQueueName

	body, err := json.Marshal(payload)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal file error payload for RabbitMQ")
		return
	}

	err = PublishToRabbit(body, queueName)
	if err != nil {
		log.Error().Str("queue", queueName).Msg("Failed to publish file error payload to queue")
	} else {
		log.Info().Str("queue", queueName).Msg("File error payload successfully published to queue")
	}
}

func PublishDataErrorToQueue(payload WebhookErrorPayload) {
	queueName := *webhookErrorQueueName
	body, err := json.Marshal(payload)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal data error payload for RabbitMQ")
		return
	}
	err = PublishToRabbit(body, queueName)
	if err != nil {
		log.Error().Str("queue", queueName).Msg("Failed to publish data error payload to queue")
	} else {
		log.Info().Str("queue", queueName).Msg("Data error payload successfully published to queue")
	}
}
