# Channels

```golang
ch := make(chan int)    // Create a channel that holds integers

ch <- 42                // Send: Data flows into the channel
val := <-ch             // Receive: Data flows out of the channel
```
