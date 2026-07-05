import { Route, Switch } from 'wouter'
import Welcome from './screens/Welcome'
import PeopleScreen from './screens/People'
import ItemsScreen from './screens/Items'
import AssignScreen from './screens/Assign'
import ResultsScreen from './screens/Results'
import SharedView from './screens/SharedView'

export default function App() {
  return (
    <Switch>
      <Route path="/" component={Welcome} />
      <Route path="/bill/:id/people" component={PeopleScreen} />
      <Route path="/bill/:id/items" component={ItemsScreen} />
      <Route path="/bill/:id/assign" component={AssignScreen} />
      <Route path="/bill/:id/results" component={ResultsScreen} />
      <Route path="/s/:token" component={SharedView} />
      <Route>
        <div className="p-6 text-center text-sm text-neutral-500">Page not found.</div>
      </Route>
    </Switch>
  )
}
