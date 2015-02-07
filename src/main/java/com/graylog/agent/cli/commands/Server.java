package com.graylog.agent.cli.commands;

import com.google.common.collect.Sets;
import com.google.common.util.concurrent.Service;
import com.google.common.util.concurrent.ServiceManager;
import com.graylog.agent.config.ConfigurationError;
import com.graylog.agent.buffer.BufferConsumer;
import com.graylog.agent.buffer.BufferProcessor;
import com.graylog.agent.buffer.MessageBuffer;
import com.graylog.agent.config.ConfigurationProcessor;
import com.graylog.agent.outputs.OutputRouter;
import io.airlift.airline.Command;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.File;
import java.util.HashSet;
import java.util.Set;

@Command(name = "server", description = "Start the agent")
public class Server implements Runnable {
    private static final Logger LOG = LoggerFactory.getLogger(Server.class);

    @Override
    public void run() {
        LOG.info("Running {}", getClass().getCanonicalName());

        final MessageBuffer buffer = new MessageBuffer(100);
        final ConfigurationProcessor configuration = ConfigurationProcessor.process(new File("config/agent.conf"), buffer);

        validateConfiguration(configuration);

        final Set<Service> services = Sets.newHashSet();
        final HashSet<BufferConsumer> consumers = Sets.<BufferConsumer>newHashSet(new OutputRouter());

        services.add(new BufferProcessor(buffer, consumers));
        services.addAll(configuration.getServices());

        final ServiceManager serviceManager = new ServiceManager(services);

        serviceManager.startAsync().awaitHealthy();

        LOG.info("Services started. {}", serviceManager.startupTimes());

        serviceManager.awaitStopped();
    }

    private void validateConfiguration(ConfigurationProcessor configurationProcessor) {
        if (!configurationProcessor.isValid()) {
            for (ConfigurationError error : configurationProcessor.getErrors()) {
                LOG.error("Configuration Error: {}", error.getMesssage());
            }

            System.exit(1);
        }
    }
}
