% Camunda Spring Boot configuration (job executor & history)

This file provides focused configuration examples for the Camunda Spring Boot starter. It contains only settings that map directly to Spring Boot properties (application.yaml / application.properties) so you can apply tuning via environment variables or a mounted config file.

Example `application.yaml` (Spring Boot):

```yaml
camunda:
  bpm:
    history-level: activity     # use 'activity' or 'audit' instead of 'full' under high load
    job-execution:
      enabled: true
      acquisition:
        - name: default
          max-jobs-per-acquisition: 5
          queue-size: 50
          wait-time-in-millis: 5000
          lock-time-in-millis: 60000
          acquire-by-due-date: false
        - name: background
          max-jobs-per-acquisition: 2
          queue-size: 20
          wait-time-in-millis: 10000
          lock-time-in-millis: 120000
          acquire-by-due-date: false
    history-cleanup:
      batch-window-start-time: "02:00"
      batch-window-end-time: "04:00"
      strategy: removalTimeBased
      # optionally tune batch-size and other cleanup parameters depending on your version
```

Notes and recommendations

- max-jobs-per-acquisition limits how many jobs a single acquisition will fetch — reduce this to lower peak DB load.
- queue-size buffers locked jobs locally; increase for long-running handlers to keep a steady pipeline.
- acquire-by-due-date: set to false when you have a large number of jobs to avoid expensive sorting by due date.
- Split job acquisition into multiple acquisition groups (different names) to separate fast vs slow workloads.
- Use `history-level: activity` or `audit` instead of `full` to reduce writes to ACT_HI_* tables.
- Enable removalTimeBased history cleanup and schedule its batch window during off-peak hours.

Practical guidance

- Test settings in staging and monitor DB load, slow queries, table growth (`ACT_RU_*`, `ACT_HI_*`) and lock contention.
- Adjust `max-jobs-per-acquisition` and `queue-size` based on handler latency and DB capacity.
- If your deployment uses environment variables, apply the same keys using Spring Boot relaxed binding (dots -> underscores, uppercase). For list entries use index notation: e.g. `CAMUNDA_BPM_JOB_EXECUTION_ACQUISITION_0_NAME=default`.

Docs: https://docs.spring.io/spring-boot/reference/features/external-config.html#features.external-config.typesafe-configuration-properties.relaxed-binding.environment-variables

If you want, I can generate a ready-to-mount `application.yaml` file tailored to your expected workload or produce an env-file mapping for the tuned values.
