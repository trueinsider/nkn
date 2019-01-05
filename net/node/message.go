package node

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/gogo/protobuf/proto"
	"github.com/nknorg/nkn/crypto"
	"github.com/nknorg/nkn/pb"
	"github.com/nknorg/nkn/util/log"
	nnetnode "github.com/nknorg/nnet/node"
	nnetpb "github.com/nknorg/nnet/protobuf"
)

func (localNode *LocalNode) SerializeMessage(unsignedMsg *pb.UnsignedMessage, sign bool) ([]byte, error) {
	if localNode.account == nil {
		return nil, errors.New("Account is nil")
	}

	buf, err := proto.Marshal(unsignedMsg)
	if err != nil {
		return nil, err
	}

	var signature []byte
	if sign {
		hash := sha256.Sum256(buf)
		signature, err = crypto.Sign(localNode.account.PrivateKey, hash[:])
		if err != nil {
			return nil, err
		}
	}

	signedMsg := &pb.SignedMessage{
		Message:   buf,
		Signature: signature,
	}

	return proto.Marshal(signedMsg)
}

func (remoteNode *RemoteNode) SendBytesAsync(buf []byte) error {
	err := remoteNode.localNode.nnet.SendBytesDirectAsync(buf, remoteNode.nnetNode)
	if err != nil {
		log.Errorf("Error sending async messge to node %v, removing node.", err.Error())
		remoteNode.CloseConn()
		remoteNode.localNode.DelNbrNode(remoteNode.GetID())
	}
	return err
}

func (remoteNode *RemoteNode) SendBytesSync(buf []byte) ([]byte, error) {
	reply, _, err := remoteNode.localNode.nnet.SendBytesDirectSync(buf, remoteNode.nnetNode)
	if err != nil {
		log.Errorf("Error sending sync messge to node: %v", err.Error())
	}
	return reply, err
}

func (remoteNode *RemoteNode) SendBytesReply(replyToID, buf []byte) error {
	err := remoteNode.localNode.nnet.SendBytesDirectReply(replyToID, buf, remoteNode.nnetNode)
	if err != nil {
		log.Errorf("Error sending async messge to node: %v, removing node.", err.Error())
		remoteNode.CloseConn()
		remoteNode.localNode.DelNbrNode(remoteNode.GetID())
	}
	return err
}

func (localNode *LocalNode) remoteMessageRouted(remoteMessage *nnetnode.RemoteMessage, nnetLocalNode *nnetnode.LocalNode, remoteNodes []*nnetnode.RemoteNode) (*nnetnode.RemoteMessage, *nnetnode.LocalNode, []*nnetnode.RemoteNode, bool) {
	if remoteMessage.Msg.MessageType == nnetpb.BYTES {
		err := localNode.maybeAddRemoteNode(remoteMessage.RemoteNode)
		if err != nil {
			log.Errorf("Add remote node error: %v", err)
		}

		for _, remoteNode := range remoteNodes {
			err = localNode.maybeAddRemoteNode(remoteNode)
			if err != nil {
				log.Errorf("Add remote node error: %v", err)
			}
		}

		msgBody := &nnetpb.Bytes{}
		err = proto.Unmarshal(remoteMessage.Msg.Message, msgBody)
		if err != nil {
			log.Errorf("Error unmarshal byte msg: %v", err)
			return nil, nil, nil, false
		}

		signedMsg := &pb.SignedMessage{}
		unsignedMsg := &pb.UnsignedMessage{}
		err = proto.Unmarshal(msgBody.Data, signedMsg)
		if err != nil {
			// TODO: remove this part after all msg are migrated to pb
			signedMsg = nil
			unsignedMsg = nil
		} else {
			err = proto.Unmarshal(signedMsg.Message, unsignedMsg)
			if err != nil {
				log.Errorf("Error unmarshal signed unsigned msg: %v", err)
				return nil, nil, nil, false
			}

			err = checkMessageType(unsignedMsg.MessageType)
			if err != nil {
				log.Errorf("Error checking message type: %v", err)
				return nil, nil, nil, false
			}

			err = checkMessageSigned(unsignedMsg.MessageType, len(signedMsg.Signature) > 0)
			if err != nil {
				log.Errorf("Error checking signed: %v", err)
				return nil, nil, nil, false
			}

			err = checkMessageRoutingType(unsignedMsg.MessageType, remoteMessage.Msg.RoutingType)
			if err != nil {
				log.Errorf("Error checking routing type: %v", err)
				return nil, nil, nil, false
			}

			if len(signedMsg.Signature) > 0 && remoteMessage.Msg.RoutingType != nnetpb.DIRECT {
				log.Errorf("Signature is only allowed on direct message")
				return nil, nil, nil, false
			}
		}

		if nnetLocalNode != nil {
			var senderNode *Node
			senderRemoteNode := localNode.getNbrByNNetNode(remoteMessage.RemoteNode)
			if senderRemoteNode != nil {
				senderNode = senderRemoteNode.Node
			} else if remoteMessage.RemoteNode == nil {
				senderNode = localNode.Node
			} else {
				log.Error("Cannot get neighbor node")
				return nil, nil, nil, false
			}

			if signedMsg == nil {
				// TODO: remove this part after all msg are migrated to pb
				err = HandleNodeMsg(senderRemoteNode, msgBody.Data)
				if err != nil {
					log.Errorf("Error handling node msg: %v", err)
					return nil, nil, nil, false
				}
				nnetLocalNode = nil
			} else {
				if len(signedMsg.Signature) > 0 {
					pubKey := senderNode.GetPubKey()
					if pubKey == nil {
						log.Errorf("Neighbor public key is nil")
						return nil, nil, nil, false
					}

					hash := sha256.Sum256(signedMsg.Message)
					err = crypto.Verify(*pubKey, hash[:], signedMsg.Signature)
					if err != nil {
						log.Errorf("Verify signature error: %v", err)
						return nil, nil, nil, false
					}
				}

				if len(remoteMessage.Msg.ReplyToId) == 0 {
					reply, err := localNode.receiveMessage(senderNode, unsignedMsg)
					if err != nil {
						log.Errorf("Error handling message: %v", err)
						return nil, nil, nil, false
					}

					if len(reply) > 0 && senderRemoteNode != nil {
						err = senderRemoteNode.SendBytesReply(remoteMessage.Msg.MessageId, reply)
						if err != nil {
							log.Errorf("Error sending reply: %v", err)
							return nil, nil, nil, false
						}
					}

					nnetLocalNode = nil
				} else {
					msgBody.Data = unsignedMsg.Message
					remoteMessage.Msg.Message, err = proto.Marshal(msgBody)
					if err != nil {
						log.Errorf("Marshal reply msg body error: %v", err)
						return nil, nil, nil, false
					}
				}
			}
		}

		if nnetLocalNode == nil && len(remoteNodes) == 0 {
			return nil, nil, nil, false
		}

		if remoteMessage.Msg.RoutingType == nnetpb.RELAY && len(remoteNodes) > 0 {
			msg, err := ParseMsg(msgBody.Data)
			if err != nil {
				log.Errorf("Parse msg error: %v", err)
				return nil, nil, nil, false
			}

			relayMsg, ok := msg.(*RelayMessage)
			if !ok {
				log.Errorf("Msg is not relay message")
				return nil, nil, nil, false
			}

			relayMsg, err = localNode.processRelayMessage(relayMsg, remoteNodes)
			if err != nil {
				log.Errorf("Process relay msg error: %v", err)
				return nil, nil, nil, false
			}

			msgBody.Data, err = relayMsg.ToBytes()
			if err != nil {
				log.Errorf("Relay msg to bytes error: %v", err)
				return nil, nil, nil, false
			}

			remoteMessage.Msg.Message, err = proto.Marshal(msgBody)
			if err != nil {
				log.Errorf("Marshal relay msg body error: %v", err)
				return nil, nil, nil, false
			}
		}
	}

	return remoteMessage, nnetLocalNode, remoteNodes, true
}

func (localNode *LocalNode) receiveMessage(sender *Node, unsignedMsg *pb.UnsignedMessage) ([]byte, error) {
	remoteMessage := &RemoteMessage{
		Sender:  sender,
		Message: unsignedMsg.Message,
	}

	var reply []byte
	var shouldCallNext bool
	var err error

	for _, handler := range localNode.GetHandlers(unsignedMsg.MessageType) {
		reply, shouldCallNext, err = handler(remoteMessage)
		if err != nil {
			log.Errorf("Get error when handling message: %v", err)
			continue
		}

		if !shouldCallNext {
			break
		}
	}

	return reply, nil
}

func (localNode *LocalNode) processRelayMessage(relayMsg *RelayMessage, remoteNodes []*nnetnode.RemoteNode) (*RelayMessage, error) {
	if len(remoteNodes) == 0 {
		return nil, fmt.Errorf("no next hop")
	}

	if len(remoteNodes) > 1 {
		return nil, fmt.Errorf("multiple next hop is not supported yet")
	}

	nextHop := localNode.getNbrByNNetNode(remoteNodes[0])
	if nextHop == nil {
		return nil, fmt.Errorf("cannot get next hop neighbor node")
	}

	relayPacket := &relayMsg.Packet
	err := localNode.relayer.SignRelayPacket(nextHop, relayPacket)
	if err != nil {
		return nil, err
	}

	relayMsg, err = NewRelayMessage(relayPacket)
	if err != nil {
		return nil, err
	}

	return relayMsg, nil
}

// checkMessageType checks if a message type is allowed
func checkMessageType(messageType pb.MessageType) error {
	if messageType == pb.MESSAGE_TYPE_PLACEHOLDER_DO_NOT_USE {
		return fmt.Errorf("message type %s should not be used", pb.MESSAGE_TYPE_PLACEHOLDER_DO_NOT_USE.String())
	}

	return nil
}

// checkMessageSigned checks if a message type is signed or unsigned as allowed
func checkMessageSigned(messageType pb.MessageType, signed bool) error {
	switch signed {
	case true:
		if _, ok := pb.AllowedSignedMessageType_name[int32(messageType)]; !ok {
			return fmt.Errorf("message of type %s should be signed", messageType.String())
		}
	case false:
		if _, ok := pb.AllowedUnsignedMessageType_name[int32(messageType)]; !ok {
			return fmt.Errorf("message of type %s should not be signed", messageType.String())
		}
	}
	return nil
}

// checkMessageRoutingType checks if a message type has the allowed routing type
func checkMessageRoutingType(messageType pb.MessageType, routingType nnetpb.RoutingType) error {
	switch routingType {
	case nnetpb.DIRECT:
		if _, ok := pb.AllowedDirectMessageType_name[int32(messageType)]; ok {
			return nil
		}
	case nnetpb.RELAY:
		if _, ok := pb.AllowedRelayMessageType_name[int32(messageType)]; ok {
			return nil
		}
	case nnetpb.BROADCAST_PUSH:
		if _, ok := pb.AllowedBroadcastPushMessageType_name[int32(messageType)]; ok {
			return nil
		}
	case nnetpb.BROADCAST_PULL:
		if _, ok := pb.AllowedBroadcastPullMessageType_name[int32(messageType)]; ok {
			return nil
		}
	case nnetpb.BROADCAST_TREE:
		if _, ok := pb.AllowedBroadcastTreeMessageType_name[int32(messageType)]; ok {
			return nil
		}
	}
	return fmt.Errorf("message of type %s is not allowed for routing type %s", messageType.String(), routingType.String())
}
