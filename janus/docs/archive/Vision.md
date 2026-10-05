Ok so lets start with the vision of where we are going. 

Janus is the name inspired by the Roman god of beginnings, gates, transitions, time, doorways, passages, etc...

I love how seemless and user friendly Vercel is in Adding New Projects, Connecting Git providers, Importing Git Providers.

I also love Digital Ocean for their Premade Deployments for Frameworks, Vercel also has this feature.

I am a huge fan of NGROk
When a tunnel is created you dont need to specify a domain, NGROK automatically assings a randomly generated domain name.
EG("https://leb2-181-80-12-3.ngrok.app"). That use case is used for quick, temporary setups. Now they also support the ability for the users to bring their own domains. app.your-domain.com by setting up a CNAME record, that points to ngroks infastructure.

My favorite is Encore.dev where using code itself generates (Services, API's, Databases, Cron Jobs, Pub/Sub, Object Storage, Caching) automagically on the backend.

This all leads me to the idea that the user can write code, then it gets brought into the system (Janus) and then gets dockerified, we run tests to determine Resource Demands. Then allows the user to run the container on their own servers, or through some sort of configuration allow the container to run on a Distributed Network of users that allow for there resources that they allow to donate or through a per minute billing (inspired by AWS) or a reward based idea like https://www.grass.io/ get rewarded monetarily or through a points system for their resources.