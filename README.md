# s3-prober

checks availability of a S3 compatible object store 

## Usage

```
export ACCESSKEY="<access-key>"
export SECRETKEY="<secret-key>"
export ENDPOINT="<url>"
export BUCKET="<test-bucket-name>"
export LOCATION="<location>"
export FILENAME="<filename-for-testing>"

./s3-prober start
```


provides following metrics:

```
# HELP probe_duration_seconds Returns how long the probe took to complete in seconds
# TYPE probe_duration_seconds gauge
probe_duration_seconds{endpoint="s3.example.com",job="s3-prober",operation="makebucket"} 0.244961068
probe_duration_seconds{endpoint="s3.example.com",job="s3-prober",operation="put"} 0.045598381
probe_duration_seconds{endpoint="s3.example.com",job="s3-prober",operation="stat"} 0.026070825
probe_duration_seconds{endpoint="s3.example.com",job="s3-prober",operation="get"} 0.051935176
probe_duration_seconds{endpoint="s3.example.com",job="s3-prober",operation="remove"} 0.038347511
probe_duration_seconds{endpoint="s3.example.com",job="s3-prober",operation="removebucket"} 1.060612757
# HELP probe_success Displays whether or not the probe was a success
# TYPE probe_success gauge
probe_success{endpoint="s3.example.com",job="s3-prober",operation="makebucket"} 1
probe_success{endpoint="s3.example.com",job="s3-prober",operation="put"} 1
probe_success{endpoint="s3.example.com",job="s3-prober",operation="stat"} 1
probe_success{endpoint="s3.example.com",job="s3-prober",operation="get"} 1
probe_success{endpoint="s3.example.com",job="s3-prober",operation="remove"} 1
probe_success{endpoint="s3.example.com",job="s3-prober",operation="removebucket"} 1
```

