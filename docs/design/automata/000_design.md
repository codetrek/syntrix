# Automabase

在syntrix里加入一个基于**状态机原理**来设计的单向数据流的触发式实时数据库.

## 状态机的结构

- 指定一个document来绑定状态机，比如: `apps/chatbot/users/{user-id}/chats/{chat-id}`
- 指定一个collection来作为状态机输入（input）（可以是上面document的一个子collection，也可以不是），比如: `apps/chatbot/users/{user-id}/chats/{chat-id}/messages`
- 用户可以设计一个automata状态机，这个状态机包括：
  - 一个默认View，包含所有的input
  - 一些Named View，每个都是input+filter得到的结果
  - 一些Named Reducer，得到一个确定的状态（不是一个array或者list），每个reducer只能从一个view计算得到状态
  - 一些Named Handler，handler可以观察一组view和reducer的变化

## 工作机制

状态机只有一个input作为输入驱动整个状态机运行，当有input输入的时候：

- 触发views更新
- 继而触发reducer执行，reducer执行结果会被缓存起来
- view更新或者reducer结果变化会触发观察它们的handler执行

## 数据模型

- Input: 必须是一个可排序的collection，document只能append到末尾。这个input **不必须** 是状态机root document的一个sub collection

- Meta document: `.../{automata}`

  ```json
  {
    "automaManaged": true,         // 这个document已经被Automa托管了，所有的syntrix restful的写操作都需要被deny
    "input": "<input collection>", // e.g.: apps/chatbot/users/{user-id}/chats/{chat-id}/messages
    "..."
  }
  ```

  状态机的root document，整个状态机的状态存放都是以它为parent

- `.../{automata}/views/{view name}/results`: 存放Named View的结果集**缓存**

  - 这是一个collection，里面可以放很多document，按固定字段（比如：timestamp）排序可以得到一个有序的列表
  - `{view name}` 记录view执行的状态:

  ```json
  {
    "automaManaged": true,
    "progress": "<progress token>", // 记录处理input的进度
    "...": "...", // 更多其他的状态
  }
  ```

- Named Reducer结果放到: `.../{automata}/redusers/{reducer name}`

  `{reducer name}` 记录reducer的状态:

  ```json
  {
    "automaManaged": true,
    "progress": "<progress token>", // 记录处理view的进度
    "state": {...}, // current state 缓存
  }
  ```

- Named Handler: `.../{automata}/redusers/{handler name}`

  `{handler name}` 记录handler执行状态：

  ```json
  {
    "automaManaged": true,
    "views": {
      "name": "<progress token>",
      // ...
    },
    "reducers": {
      "name": "<progress token>",
      // ...
    }
  }
  ```

## 工作方式

- View: 一组input上的过滤器，生成一个数据库层视图，它由Automabase处理，不需要回调
- Reducer: Webhook，业务定义的一个无状态函数，从view生成一个state，需要严格控制处理时间：
  - 输入：{Current State} + {View Changes}

  ```json
  {
    "currentState": {...},
    "changes": [{...}, ...]
  }
  ```

  - 输出：
    - Success + {New State} Or
    - Failed, 一会重试

    ```json
    {
      "status": Success|Failed,
      "newState": {...} // 当status=Success时存在
    }
    ```

- Handler: Webhook, 业务定义的复杂运算，定义为有自己状态，当收到webhook后需要记录状态并**快速ack**:
  - 输入:

    ```json
    {
      "views": {
        "{name}": [{<change>}, ...],
        // ...
      },
      "reducer": {
        "name": {<New State>},
        // ...
      }
    }
    ```

  - 输出: Success or Fail

    返回Success表示Handler以成功收到了change，会在后台慢慢处理，处理后对状态机新输入会append到input里。

## Restful接口

### 管理Automata

- Create: `POST /automata/v1/create`
- Get: `GET /automata/v1/<name>`
- Query: `GET /automata/v1/?<filters>`
- Update: `PUT /automata/v1/<name>`
- Delete: `DELETE /automata/v1/<name>`

### Mount/Unmount Autometa

- Mount: `POST /automata/v1/mount` - 在document上mount状态机，这个document会被Automata接管
- Unmount: `POST /automata/v1/unmount` - 从document上unmount状态机

注意: Mount / Unmount 是对一个path pattern做，而不是单个独立的document，然后系统会自动匹配任何已经存在或者将来新创建的document，比如:

  **Mount**: users/{uid}/chats/{chat-id}
