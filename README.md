Note that all of this was tested with a self-hosted community edition of SigNoz.

As of version 0.103.0, SigNoz can be used as a Prometheus data source in Grafana. The way to do it is this:

1. Create a new Prometheus data source and add the address of your instance of SigNoz

![alt text](imgs/image.png)

2. In SigNoz UI, generate an API key with the viewer role and in the Grafana's setup for the Prometheus data source add this header 

![alt text](imgs/image-1.png)

3. In the Other section change HTTP method to GET

![alt text](imgs/image-2.png)

Then if you press Save & test, the data source should work.

Now you should be able to create a new visualization with this data source and query SigNoz. Unfortunately SigNoz doesn't implement the APIs which Grafana uses for suggesting existing time series and labels, so you can't use those features.

This app implements those missing APIs and acts as a proxy between Grafana and SigNoz. If you deploy it and point the Prometheus data source to the Docker container with other settings set according to the instructions above, the app will call the existing SigNoz API and return data in the format compatible with Prometheus. Then you should be able to see suggestions for existing metrics and labels is Grafana.

The address of your SigNoz instance can be set using the `SIGNOZ_URL` environment variable. If you use the `docker-compose.yaml` file from this repository on the same machine where SigNoz is running, it should work out of the box.