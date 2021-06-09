# flyvo-calendar-queue

 **Introduction**
Flyvo-calendar-queue is an optional service that were created because of limitations in google APIs. They have restrictions on max number of event creations/invites we can do. Some users might never reach these limits, but for us this was a big problem so we had to create a queue system that limited the numbers of request we sent to google. We use activemq to store the messages. 

It will try to send requests until it reaches the limit, and the start a sleep process you can configure yourself in the config file. In our experience the values we have in /dev_cfg/cfg.yml worked the best for us with a 4h min/max sleep time once we reached the limit.

If you have no issue with the limits, you can skip running this integration.

**How to build**

1. Build as a runable file

We use make to build our projects. You can define what system to build for by configuring the GOOS environment variable.

  

>\> GOOS=windows make clean build

  

>\> GOOS=linux make clean build

  

These commands will build either a runable linux or windows file in the /bin/amd64 folder

  

2. Build a docker container

First you have to define the docker registry you are going to use in "envfile". Replace the REGISTRY variable with your selection.

Run the following command

  

>\> GOOS=linux make clean build container push

  

This will build the container and push it to your docker registry.

  

**How to run**

1. Executable

If you want to run it as a executable (Windows service etc) you will need to configure the correct environment variable. When you start the application set the CONFIG environment to **file::\<location\>**

  

Windows example: **set "CONFIG=file::Z://folder//cfg.yml" & flyvo-calendar-queue.exe**

Linux example: **CONFIG=file::../folder/cfg.yml ./flyvo-calendar-queue**

  

2. Docker

You have to first mount the cfg file into the docker container, and then set the config variable to point to that location before running the service/container

**Configuration file**

  

**debug:** Set to true/false to enable debugging of application

**calendarCreateEventUrl:** Endpoint to send queued messages to when they are dequeued.

**queueName:** Name of queue to store messages in on activemq

**errorQueueName:** If something goes wrong while handling the message. What queue should they be stored on.

**activemqBrokers:** Url to amq instances. It can be a list containing multiple instances if you use failover.

**backoffBase:** If you reach google rate limit, what base should we use as backoff.

**baseBackoffTime:** Minimum time to wait after we are rate limited.

**maxBackOff:** Maximum time to wait after we are rate limited

**dontUseBackoff:** Will turn of the dynamic backoff, but will still use baseBackoffTime when rate limit occures.

**exponentBase:** What base should we use between each time we send to google calendar.

**baseWaitTime:** Minimum wait time between sending messages to google calendar. If rate limited the backoff values are used instead.

**maxSleepTime:** Maximum wait time between sending messages to google calendar. If rate limited the backoff values are used instead.
