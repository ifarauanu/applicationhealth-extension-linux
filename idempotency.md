AHE Restarts on Every Enable Call

Analysis of Application Health Extension (AHE) Handler Enable Behavior and Proposals

Table of Contents

Issue Overview

Proposals and Evaluation

Implementation Details

Conclusion

Appendix

Issue Overview

The Application Health Extension (AHE) exhibits an unexpected behavior where, on every execution of the enable command (enable.cmd), it first calls the disable command (disable.cmd) before proceeding with the enable logic. The disable.cmd further invokes a PowerShell script (disable.ps1) that ultimately terminates all existing processes managed by AHE.

This process results in AHE killing existing processes every time the enable command is run, regardless of whether those processes are healthy or should remain running. Such behavior does not align with the contract point 2.3.2, which specifies that the enable command should be idempotent: if the handler is already enabled and the enable command is invoked again, it should verify that all processes are running as expected and exit successfully if so, instead of restarting or killing existing processes.

Currently, AHE’s implementation does not respect the idempotency requirements, resulting in unnecessary restarts of application processes on each enable command execution, which finally lead to failed Rolling Upgrades, and implicitly, failed Auto OS Upgrades.  

The impact is limited to scale sets in which the Guest Agent triggers a lot of extension enable requests (see Kusto query - 3 enable requests in 15-minute interval with no sequence number increase) and the Rolling Upgrade pre-batch health check runs after the extension was just started, leading to a failed health check, given that while a VM is in Initializing state, only the post-batch health check is waiting for the VM to become Healthy/Unhealthy, and the pre-batch health check only expects Healthy, without distinguishing between Initializing/Unhealthy. Considering the various number of events that lead to this outcome, it is hard to come up with an approximate number of affected scale sets; however, this is the first customer report of this kind as of January 2026.  

Proposals and Evaluation

Proposal

Pros

Cons

Update Enable Logic for Idempotency
      Modify the enable.cmd and supporting scripts to check the status of managed processes. If processes are running and healthy, exit with a success code; only start new processes if necessary.

Aligns with contract requirements for idempotency.
        Prevents unnecessary downtime and process restarts.
        Improves reliability for users relying on persistent health checks.

Requires changes in script logic.

Requires thorough testing.

No other extension calls disable.ps1 from enable.cmd script:

file:enable ext:cmd - Search Code - Search

Why does the enable script call disable.ps1?

This happens for 7 years now, since this PR was merged: Pull request 756182: Tcp support - other minor fixes - Repos

Implementation Details

Update Enable Logic for Idempotency
This solution consists of the following changes:

Removing the call to disable.ps1 from enable.cmd

Ensuring AHE idempotency by checking the sequence number and the already existing processes

Consider checking if the existing process is running and healthy.

Ensure that existing processes with older seq number kill the children processes too.

Conclusion

The current AHE enable behavior does not comply with the expected idempotent contract and leads to unnecessary process restarts. Adopting the above proposal would enhance system reliability, reduce downtime, and ensure compliance with Guest Agent extension handler guidelines.

Frank can provide primary review, and Nathan can provide secondary review.

Testing

All tests are performed with VMWatch enabled.

The unhealthy process in this testing will be simulated by a process which doesn’t log to the AppHealthExtension.log file. Thus, the healthiness check will fail. Also, the unhealthy process does not exit when it detects it has a stale sequence number, so we validate that the healthy process will properly terminate the unhealthy processes.

Test case 1 - Existing AHE process with same seq number & healthy process (initial bug we wanted to solve)

Expected outcome: Existing process continues execution; new process is being terminated at startup.

Existing process is up & running – as per the log file which is correctly updated with the heartbeat

New process detects existing process (isHandlerStillExecuting: true) and detects its healthiness (log file was fresh at startup)

New process with same sequence number is stopped to maintain idempotency

Logs from manual testing below:

[7408+00000001] [01/30/2026 16:42:28.23] [INFO] Existing process log file was fresh at startup. Last update: 2026-01-30T16:42:27.7045986Z UTC.

[7408+00000001] [01/30/2026 16:42:28.23] [INFO] IsHandlerStillExecuting: Process Name='AppHealthExtension', PID=7408, result=True

[7408+00000001] [01/30/2026 16:42:28.23] [INFO] Another instance of AppHealthExtension is already running with the same sequence number (20) and is responsive. PID 7408 exiting to maintain idempotency.

Test case 2 - Existing AHE process with same seq number & unhealthy process

Expected outcome: New process takes over execution; existing unhealthy process will be terminated by the new healthy process.

New process logs

[7004+00000001] [01/30/2026 16:47:06.81] [INFO] [INFO] Retrieved log file last write time: 2026-01-30T12:12:20.2099176Z from 'ef891391-6620-4760-aebd-755f783d789fAppHealthExtension.log' (checked 17 file(s), attempt 1)

[7004+00000001] [01/30/2026 16:47:06.82] [WARN] Existing process log file was stale at startup. Last update: 2026-01-30T12:12:20.2099176Z UTC, Threshold: 6 minutes. Process may be stuck.

[7004+00000001] [01/30/2026 16:47:06.82] [INFO] IsHandlerStillExecuting: Process Name='AppHealthExtension', PID=7004, result=True

[7004+00000001] [01/30/2026 16:47:06.82] [WARN] Another instance of AppHealthExtension exists with the same sequence number (21) but appears unresponsive (log file stale). PID 7004 taking over execution.

Old process – no logs, as it’s an unhealthy existing process

Test case 3 - Existing AHE process with lower seq number & healthy process

Expected outcome: New process (higher seq) starts and continues. Old process (lower seq) will detect stale seq on its next loop iteration and exit gracefully after cleaning up child processes

Old process

[5492+00000001] [02/05/2026 11:03:27.50] [WARN] Current sequence number, 36, is not greater than the sequence number of the most recently executed configuration (37). PID 5492 initiating graceful shutdown...

[5492+00000001] [02/05/2026 11:03:27.54] [INFO] Successfully killed VMWatch process with PID 1072. Exited: True

[5492+00000001] [02/05/2026 11:03:37.55] [INFO] Killed C:\Packages\Plugins\Microsoft.ManagedServices.ApplicationHealthWindows\2.0.23\bin\DotNetLogger\win-x64\DotNetLogger.exe. PID = 1668. Exited: True

New process

As it can be seen below, retrieving the last time the log file was updated is limited to the previous processes' updates (aka, the new process is not writing any log to the AppHealthExtension.log file before retrieving the last update time)

First log:

[6920+00000001] [02/05/2026 11:03:25.01] [INFO] Temporary directory for event files was successfully created: C:\WindowsAzure\Logs\Plugins\Microsoft.ManagedServices.ApplicationHealthWindows\Events\Temp

Following logs:  

[6920+00000001] [02/05/2026 11:03:25.01] [INFO] [INFO] Retrieved log file last write time: 2026-02-05T11:03:24.5256957Z from 'AppHealthExtension.log' (checked 27 file(s), attempt 1)

[6920+00000001] [02/05/2026 11:03:25.02] [INFO] Loading configuration for sequence number 37

The new process was properly initialized and the old process with lower sequence number has detected that it has a stale sequence number, so it cleaned up the child processes (VMWatch & DotNetLogger), after which it exited.

Test case 4 - Existing AHE process with lower seq number & unhealthy process

Expected outcome: New process starts and terminates the unhealthy process

Old process

[6024+00000001] [01/30/2026 17:32:24.38] [INFO] Cleaning up child processes before exiting due to stale sequence number...

[6024+00000001] [01/30/2026 17:32:24.38] [INFO] Done cleaning up child processes.

[6024+00000001] [01/30/2026 17:32:24.38] [WARN] Current sequence number, 24, is not greater than the sequence number of the most recently executed configuration (25). PID 6024 initiating graceful shutdown...

New process

[6152+00000001] [01/30/2026 17:32:17.33] [INFO] Loading configuration for sequence number 25

Test case 5 - Existing AHE process with higher seq number & healthy process

Expected outcome: New process (lower seq) exits immediately at startup.

Old process – continued execution as expected

New process  

[880+00000001] [01/30/2026 17:36:28.31] [WARN] Current sequence number, 25, is not greater than the sequence number of the most recently executed configuration (26). PID 880 initiating graceful shutdown...

Test case 6 - Existing AHE process with higher seq number & unhealthy process

Expected outcome: Same as Test case 5. New process (lower seq) still exists - sequence number takes precedence over healthiness, otherwise we might spawn new AHE with older settings (not desirable).

Similar as above.

Test case 7 - Non-existing AHE process (log file not recently updated/non-existing)

Expected outcome (like the current production behavior, no change in solution): New executable should be created as expected, as it will be the only AHE process

No existing AHE process -> new one is created successfully

Logs from manual testing below:

Logs: [9088+00000001] [01/27/2026 15:51:10.85] [WARN] Existing process log file was stale at startup. Last update: 2026-01-27T15:39:28.9232308Z UTC, Threshold: 6 minutes. Process may be stuck.

[9088+00000001] [01/27/2026 15:51:10.86] [INFO] IsHandlerStillExecuting: Process Name='AppHealthExtension', result=False

Confirmation that reading last health update time is not reading any update that the new process has created, but the older process’ update

[8608+00000001] [01/30/2026 16:30:35.77] [INFO] VMWatch is disabled.

[4240+00000001] [01/30/2026 16:38:51.48] [INFO] Temporary directory for event files was successfully created: C:\WindowsAzure\Logs\Plugins\Microsoft.ManagedServices.ApplicationHealthWindows\Events\Temp

[4240+00000001] [01/30/2026 16:38:51.50] [INFO] Space available in event directory: 39958380B

[4240+00000001] [01/30/2026 16:38:51.50] [INFO] Setting event reporting interval to 10000ms

[4240+00000001] [01/30/2026 16:38:51.50] [INFO] Event polling is starting...

[4240+00000001] [01/30/2026 16:38:51.51] [INFO] An ExtensionEventLogger was created

[4240+00000001] [01/30/2026 16:38:51.51] [INFO] [INFO] Retrieved log file last write time: 2026-01-30T16:30:35.7702903Z from 'AppHealthExtension.log' (checked 18 file(s), attempt 1)

Even though the file has written logs when it started at 16:38, the last update that it read was at 16:30, when the last process ran.

Appendix

Incident: Incident 706678785 : AHE restarts on every enable call

[Windows solution (draft PR): Pull request 14427775: Enable script should be idempotent - Repos](https://msazure.visualstudio.com/One/_git/Compute-ART-ManagedServiceExtensions/pullrequest/14427775)

If you can't access the PR above, these are the main changes for the windows solution (just for context, you might not need to do all these or you might need to do more changes, according to the current implementation):

        public const int RecordAppHealthHeartBeatIntervalInMinutes = 5;
        /// <summary>
        /// Number of minutes to allow between log file updates before considering the existing process as unresponsive/stuck.
        /// If the log file hasn't been updated within this interval, a new instance should take over, whenever started by Guest Agent.
        /// This should be greater than RecordAppHealthHeartBeatIntervalInMinutes to allow for timing variations.
        /// </summary>
        public const int AppHealthLogFileStaleThresholdInMinutes = 6;

                /// <summary>
        /// Flushes trace listeners and waits for the event queue to process before exiting.
        /// This ensures all logs are written to files and emitted to Kusto before the process terminates.
        /// </summary>
        private void FlushAndExit(int exitCode)
        {
            Trace.Flush();
            Thread.Sleep(Constants.TelemetryEventReportingIntervalInMilliSeconds);
            Environment.Exit(exitCode);
        }


              public void ExitOnStaleSequenceNumber(long sequenceNumber)
        /// <summary>
        /// Checks if the current sequence number is stale (older than the most recently started).
        /// If stale, executes cleanup action (if provided) and exits the process.
        /// </summary>
        /// <param name="sequenceNumber">The current sequence number</param>
        /// <param name="cleanupBeforeExit">Optional cleanup action to perform before exiting</param>
        public void ExitOnStaleSequenceNumber(long sequenceNumber, Action cleanupBeforeExit = null)
        {
            if (sequenceNumber < MostRecentSequenceNumberStarted)
            {
                ExtensionEventLogger.LogStdOutAndEvent(LogSeverityLevel.WARN, "Current sequence number, {0}, is not greater than the sequence number of the most recently executed configuration. Exiting...", sequenceNumber);
                Environment.Exit(Constants.ExitCode_Okay);
                ExtensionEventLogger.LogStdOutAndEvent(LogSeverityLevel.WARN, 
                    "Current sequence number, {0}, is not greater than the sequence number of the most recently executed configuration ({1}). PID {2} initiating graceful shutdown...", 
                    sequenceNumber, MostRecentSequenceNumberStarted, Process.GetCurrentProcess().Id);
                // Execute cleanup action before exiting (e.g., kill VMWatch, DotNetLogger child processes)
                cleanupBeforeExit?.Invoke();
                FlushAndExit(Constants.ExitCode_Okay);
            }
        }
        /// <summary>
        /// Checks if another instance with the same sequence number is already running and responsive.
        /// If so, exits to maintain idempotency.
        /// </summary>
        /// <param name="sequenceNumber">The current sequence number</param>
        /// <param name="isHandlerStillExecuting">Function to check if another handler instance is running</param>
        /// <param name="isExistingProcessResponsive">Whether the existing handler is responsive (based on log file activity)</param>
        public void ExitIfDuplicateInstance(long sequenceNumber, Func<bool> isHandlerStillExecuting, bool isExistingProcessResponsive)
        {
            if (sequenceNumber == MostRecentSequenceNumberStarted && isHandlerStillExecuting() && isExistingProcessResponsive)
            {
                int currentPid = Process.GetCurrentProcess().Id;
                ExtensionEventLogger.LogStdOutAndEvent(LogSeverityLevel.INFO,
                    "Another instance of AppHealthExtension is already running with the same sequence number ({0}) and is responsive. PID {1} exiting to maintain idempotency.",
                    sequenceNumber, currentPid);
                FlushAndExit(Constants.ExitCode_Okay);
            }
        }


        Problem
The Application Health Extension (AHE) exhibits an unexpected behavior where, on every execution of the enable command (enable.cmd), it first calls the disable command (disable.cmd) before proceeding with the enable logic. The disable.cmd further invokes a PowerShell script (disable.ps1) that ultimately terminates all existing processes managed by AHE.

This process results in AHE killing existing processes every time the enable command is run, regardless of whether those processes are healthy or should remain running. Such behavior does not align with the contract point 2.3.2, which specifies that the enable command should be idempotent: if the handler is already enabled and the enable command is invoked again, it should verify that all processes are running as expected and exit successfully if so, instead of restarting or killing existing processes.

Currently, AHE’s implementation does not respect the idempotency requirements, resulting in unnecessary restarts of application processes on each enable command execution, which finally lead to failed Rolling Upgrades, and implicitly, failed Auto OS Upgrades.

The impact is limited to scale sets in which the Guest Agent triggers a lot of extension enable requests (see Kusto query - 3 enable requests in 15-minute interval with no sequence number increase) and the Rolling Upgrade pre-batch health check runs after the extension was just started, leading to a failed health check, given that while a VM is in Initializing state, only the post-batch health check is waiting for the VM to become Healthy/Unhealthy, and the pre-batch health check only expects Healthy, without distinguishing between Initializing/Unhealthy. Considering the various number of events that lead to this outcome, it is hard to come up with an approximate number of affected scale sets; however, this is the first customer report of this kind as of January 2026.

(see AHE Restarts on Every Enable Call )

Solution
Update Enable Logic for Idempotency

Modify the enable.cmd to remove the call to disable.cmd and update supporting scripts to check the status of managed processes. If processes are running and healthy, exit with a success code; only start new processes if necessary.

When enable.cmd is called with the latest sequence number (so no need to terminate the older process, just keep running that one), it is crucial that we check if the existing process is running and healthy

This is being done by checking the last time the log file was updated. There is a heartbeat that logs every 5 minutes. When the new process spawns, before doing anything else we check the last time the log file was updated (this is done to ensure that the new process doesn't write any log so that it detects its own log when checking last update time). If the log time wasn't updated in the past 6 minutes (heartbeat time 5 mins + 1 minute margin of error), we consider the existing process unhealthy, so the new one should actually take over.
